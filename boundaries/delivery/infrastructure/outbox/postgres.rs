//! `delivery_outbox` access of the relay on its own async pool, and the
//! session-level advisory lock of the single active instance.

use std::time::Duration;

use sqlx::postgres::{PgConnection, PgPool, PgPoolOptions};
use sqlx::{Connection as _, Row};
use tokio::sync::Mutex;

use super::relay::{OutboxEntry, OutboxStore};
use crate::pkg::config::{DATABASE_URL_ENV, DatabaseConfig};

/// `pg_try_advisory_lock` key of `delivery-relay` (ASCII "dlvrelay").
pub const RELAY_LOCK_KEY: i64 = 0x646c_7672_656c_6179;

const ACQUIRE_TIMEOUT: Duration = Duration::from_secs(5);

/// Relay connections are few: one held by the lock, one for the cycle.
const MAX_RELAY_CONNECTIONS: u32 = 4;

pub struct PgOutbox {
    pool: PgPool,
    /// Dedicated connection holding the session-level lock.
    lock: Mutex<Option<PgConnection>>,
}

fn storage(error: sqlx::Error) -> String {
    format!("postgres: {error}")
}

impl PgOutbox {
    /// An empty URL or an unreachable database fails startup.
    pub async fn connect(config: &DatabaseConfig) -> Result<Self, String> {
        if !config.enabled() {
            return Err(format!("{DATABASE_URL_ENV} is required"));
        }
        let pool = PgPoolOptions::new()
            .max_connections(config.max_conns.min(MAX_RELAY_CONNECTIONS))
            .acquire_timeout(ACQUIRE_TIMEOUT)
            .connect(&config.url)
            .await
            .map_err(|error| format!("connect postgres: {error}"))?;
        Ok(Self {
            pool,
            lock: Mutex::new(None),
        })
    }

    /// `true` while this instance holds the lock. A held lock is verified on
    /// its connection; a broken connection loses the lock.
    pub async fn try_acquire_lock(&self) -> Result<bool, String> {
        let mut held = self.lock.lock().await;
        if let Some(connection) = held.as_mut() {
            match connection.ping().await {
                Ok(()) => return Ok(true),
                Err(error) => {
                    *held = None;
                    return Err(format!("lock connection lost: {error}"));
                }
            }
        }
        let mut connection = self.pool.acquire().await.map_err(storage)?.detach();
        let acquired: bool = sqlx::query_scalar("SELECT pg_try_advisory_lock($1)")
            .bind(RELAY_LOCK_KEY)
            .fetch_one(&mut connection)
            .await
            .map_err(storage)?;
        if acquired {
            *held = Some(connection);
        } else {
            let _ = connection.close().await;
        }
        Ok(acquired)
    }

    /// `true` while the session holding the lock is alive (FR-537). Never
    /// acquires: a relay that did not hold the lock keeps not holding it. A
    /// broken lock connection is dropped, so the next cycle re-acquires.
    pub async fn lock_alive(&self) -> bool {
        let mut held = self.lock.lock().await;
        let Some(connection) = held.as_mut() else {
            return false;
        };
        match connection.ping().await {
            Ok(()) => true,
            Err(error) => {
                eprintln!("outbox lock connection lost: {error}");
                *held = None;
                false
            }
        }
    }

    pub async fn release_lock(&self) {
        if let Some(mut connection) = self.lock.lock().await.take() {
            let _ = sqlx::query("SELECT pg_advisory_unlock($1)")
                .bind(RELAY_LOCK_KEY)
                .execute(&mut connection)
                .await;
            let _ = connection.close().await;
        }
    }

    /// `(topic, pending, oldest pending age in seconds)` of unpublished rows.
    pub async fn backlog(&self) -> Result<Vec<(String, u64, f64)>, String> {
        let rows = sqlx::query(
            "SELECT topic, count(*) AS pending,
                 EXTRACT(EPOCH FROM now() - min(occurred_at))::float8 AS age
             FROM delivery_outbox WHERE published_at IS NULL GROUP BY topic",
        )
        .fetch_all(&self.pool)
        .await
        .map_err(storage)?;
        rows.iter()
            .map(|row| {
                let pending: i64 = row.try_get("pending")?;
                Ok((
                    row.try_get("topic")?,
                    u64::try_from(pending).unwrap_or_default(),
                    row.try_get("age")?,
                ))
            })
            .collect::<Result<_, sqlx::Error>>()
            .map_err(storage)
    }

    /// Deletes published rows older than `retention`; unpublished rows stay.
    pub async fn cleanup(&self, retention: Duration) -> Result<u64, String> {
        sqlx::query(
            "DELETE FROM delivery_outbox
             WHERE published_at IS NOT NULL AND published_at < now() - make_interval(secs => $1)",
        )
        .bind(retention.as_secs_f64())
        .execute(&self.pool)
        .await
        .map(|done| done.rows_affected())
        .map_err(storage)
    }
}

impl OutboxStore for PgOutbox {
    async fn holds_lock(&self) -> bool {
        self.lock_alive().await
    }

    async fn fetch(&self, limit: u32) -> Result<Vec<OutboxEntry>, String> {
        let rows = sqlx::query(
            "SELECT seq, event_id::text AS event_id, event_type, aggregate_type, aggregate_id,
                 aggregate_version,
                 to_char(occurred_at AT TIME ZONE 'UTC', 'YYYY-MM-DD\"T\"HH24:MI:SS.US') AS occurred_at,
                 EXTRACT(EPOCH FROM occurred_at)::float8 AS occurred_at_epoch,
                 topic, payload, metadata->>'traceparent' AS traceparent
             FROM delivery_outbox WHERE published_at IS NULL ORDER BY seq LIMIT $1",
        )
        .bind(i64::from(limit))
        .fetch_all(&self.pool)
        .await
        .map_err(storage)?;
        rows.iter()
            .map(|row| {
                Ok(OutboxEntry {
                    seq: row.try_get("seq")?,
                    event_id: row.try_get("event_id")?,
                    event_type: row.try_get("event_type")?,
                    aggregate_type: row.try_get("aggregate_type")?,
                    aggregate_id: row.try_get("aggregate_id")?,
                    aggregate_version: row.try_get("aggregate_version")?,
                    occurred_at: row.try_get("occurred_at")?,
                    occurred_at_epoch: row.try_get("occurred_at_epoch")?,
                    topic: row.try_get("topic")?,
                    payload: row.try_get("payload")?,
                    traceparent: row.try_get("traceparent")?,
                })
            })
            .collect::<Result<_, sqlx::Error>>()
            .map_err(storage)
    }

    async fn mark_published(&self, seq: i64) -> Result<(), String> {
        sqlx::query("UPDATE delivery_outbox SET published_at = now() WHERE seq = $1")
            .bind(seq)
            .execute(&self.pool)
            .await
            .map(drop)
            .map_err(storage)
    }

    async fn record_failure(&self, seq: i64, error: &str) -> Result<(), String> {
        sqlx::query(
            "UPDATE delivery_outbox SET attempts = attempts + 1, last_error = $2 WHERE seq = $1",
        )
        .bind(seq)
        .bind(error)
        .execute(&self.pool)
        .await
        .map(drop)
        .map_err(storage)
    }
}

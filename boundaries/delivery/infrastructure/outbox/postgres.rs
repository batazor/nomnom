//! `delivery_outbox` access of the relay on its own async pool, and the
//! session-level advisory lock of the single active instance.
//!
//! Lock liveness is an ownership check, not a ping: `pg_locks` must show the
//! advisory lock of [`RELAY_LOCK_KEY`] granted to this very backend
//! (`pg_backend_pid()`). The lock connection is dedicated and bounded: every
//! statement on it has a `statement_timeout`, the client gives up after
//! [`LOCK_CHECK_TIMEOUT`], and server-side TCP keepalives make PostgreSQL drop
//! a session whose client vanished, which releases the lock for a standby.

use std::str::FromStr as _;
use std::time::Duration;

use std::sync::Arc;

use sqlx::postgres::{PgConnectOptions, PgConnection, PgPool};
use sqlx::{Connection as _, Row};
use tokio::sync::Mutex;

use super::relay::{OutboxEntry, OutboxStore};
use crate::infrastructure::postgres::{ObservedConnection, PoolMetrics, pool_options};
use crate::pkg::config::{DATABASE_URL_ENV, DatabaseConfig};

/// `pg_try_advisory_lock` key of `delivery-relay` (ASCII "dlvrelay").
pub const RELAY_LOCK_KEY: i64 = 0x646c_7672_656c_6179;

/// Upper bound of one ownership check on the lock connection, so a
/// half-open connection cannot hang the relay before a record.
pub const LOCK_CHECK_TIMEOUT: Duration = Duration::from_secs(2);

/// Session settings of the lock connection: statements are bounded on the
/// server too, and keepalives let the server notice a dead relay.
const LOCK_SESSION_OPTIONS: [(&str, &str); 5] = [
    ("statement_timeout", "2000"),
    ("tcp_keepalives_idle", "10"),
    ("tcp_keepalives_interval", "5"),
    ("tcp_keepalives_count", "3"),
    ("tcp_user_timeout", "25000"),
];

/// `pg_locks` columns of a bigint advisory lock key: `classid` holds the
/// high 32 bits, `objid` the low 32 bits and `objsubid` is 1.
#[must_use]
pub fn advisory_lock_parts(key: i64) -> (u32, u32) {
    let bits = u64::from_ne_bytes(key.to_ne_bytes());
    let high = u32::try_from(bits >> 32).unwrap_or_default();
    let low = u32::try_from(bits & u64::from(u32::MAX)).unwrap_or_default();
    (high, low)
}

/// `true` when the session of `connection` itself holds the bigint advisory
/// lock `key` in the current database. Another session holding it is `false`.
pub async fn holds_advisory_lock(
    connection: &mut PgConnection,
    key: i64,
) -> Result<bool, sqlx::Error> {
    let (classid, objid) = advisory_lock_parts(key);
    sqlx::query_scalar(
        "SELECT EXISTS (
             SELECT 1 FROM pg_locks
             WHERE locktype = 'advisory' AND granted AND pid = pg_backend_pid()
               AND database = (SELECT oid FROM pg_database WHERE datname = current_database())
               AND classid = $1::bigint::oid AND objid = $2::bigint::oid AND objsubid = 1)",
    )
    .bind(i64::from(classid))
    .bind(i64::from(objid))
    .fetch_one(connection)
    .await
}

/// Connect options of the dedicated lock connection.
pub fn lock_connect_options(url: &str) -> Result<PgConnectOptions, String> {
    PgConnectOptions::from_str(url)
        .map(|options| options.options(LOCK_SESSION_OPTIONS))
        .map_err(|error| format!("database url: {error}"))
}

/// The pool serves the cycle (Helm: `DATABASE__MAX_CONNS` 2); the lock has
/// its own connection outside the pool, so the relay opens `max_conns + 1`.
pub struct PgOutbox {
    pool: Arc<PgPool>,
    metrics: PoolMetrics,
    acquire_timeout: Duration,
    lock_options: PgConnectOptions,
    /// Dedicated connection holding the session-level lock.
    lock: Mutex<Option<PgConnection>>,
}

fn storage(error: sqlx::Error) -> String {
    format!("postgres: {error}")
}

/// Ownership check bounded by [`LOCK_CHECK_TIMEOUT`]; a failed or timed out
/// check is an error.
async fn check_ownership(connection: &mut PgConnection) -> Result<bool, String> {
    match tokio::time::timeout(
        LOCK_CHECK_TIMEOUT,
        holds_advisory_lock(connection, RELAY_LOCK_KEY),
    )
    .await
    {
        Ok(Ok(owned)) => Ok(owned),
        Ok(Err(error)) => Err(storage(error)),
        Err(_) => Err(format!("lock check timed out after {LOCK_CHECK_TIMEOUT:?}")),
    }
}

impl PgOutbox {
    /// An empty URL or an unreachable database fails startup.
    pub async fn connect(config: &DatabaseConfig) -> Result<Self, String> {
        if !config.enabled() {
            return Err(format!("{DATABASE_URL_ENV} is required"));
        }
        let lock_options = lock_connect_options(&config.url)?;
        let metrics = PoolMetrics::global();
        let pool = Arc::new(
            metrics
                .instrument(pool_options(config, config.max_conns))
                .connect(&config.url)
                .await
                .map_err(|error| format!("connect postgres: {error}"))?,
        );
        metrics.observe(&pool);
        Ok(Self {
            metrics,
            pool,
            acquire_timeout: config.acquire_timeout,
            lock_options,
            lock: Mutex::new(None),
        })
    }

    /// `true` while this instance holds the lock. A held lock is verified by
    /// the ownership check; a lost one is dropped and acquired anew.
    pub async fn try_acquire_lock(&self) -> Result<bool, String> {
        let mut held = self.lock.lock().await;
        if let Some(connection) = held.as_mut() {
            match check_ownership(connection).await {
                Ok(true) => return Ok(true),
                Ok(false) => {
                    tracing::warn!(
                        operation = "outbox.lock",
                        "outbox lock is no longer held by this session"
                    );
                }
                Err(error) => {
                    tracing::warn!(operation = "outbox.lock", error.message = %error, "outbox lock connection lost");
                }
            }
            *held = None;
        }
        let mut connection = tokio::time::timeout(
            self.acquire_timeout,
            PgConnection::connect_with(&self.lock_options),
        )
        .await
        .map_err(|_| format!("lock connection timed out after {:?}", self.acquire_timeout))?
        .map_err(storage)?;
        let acquired = tokio::time::timeout(
            self.acquire_timeout,
            sqlx::query_scalar::<_, bool>("SELECT pg_try_advisory_lock($1)")
                .bind(RELAY_LOCK_KEY)
                .fetch_one(&mut connection),
        )
        .await
        .map_err(|_| {
            format!(
                "lock acquisition timed out after {:?}",
                self.acquire_timeout
            )
        })?
        .map_err(storage)?;
        if acquired {
            *held = Some(connection);
        } else {
            let _ = connection.close().await;
        }
        Ok(acquired)
    }

    /// `true` while this backend owns the advisory lock (FR-537): `pg_locks`
    /// shows it granted to `pg_backend_pid()`. Never acquires: a relay that
    /// did not hold the lock keeps not holding it. A failed, timed out or
    /// negative check drops the connection, so the next cycle re-acquires.
    pub async fn lock_alive(&self) -> bool {
        let mut held = self.lock.lock().await;
        let Some(connection) = held.as_mut() else {
            return false;
        };
        match check_ownership(connection).await {
            Ok(true) => true,
            Ok(false) => {
                tracing::warn!(
                    operation = "outbox.lock",
                    "outbox lock is no longer held by this session"
                );
                *held = None;
                false
            }
            Err(error) => {
                tracing::warn!(operation = "outbox.lock", error.message = %error, "outbox lock connection lost");
                *held = None;
                false
            }
        }
    }

    pub async fn release_lock(&self) {
        if let Some(mut connection) = self.lock.lock().await.take() {
            let _ = tokio::time::timeout(
                LOCK_CHECK_TIMEOUT,
                sqlx::query("SELECT pg_advisory_unlock($1)")
                    .bind(RELAY_LOCK_KEY)
                    .execute(&mut connection),
            )
            .await;
            let _ = tokio::time::timeout(LOCK_CHECK_TIMEOUT, connection.close()).await;
        }
    }

    /// A pooled connection for one statement (pool metrics recorded).
    async fn connection(&self) -> Result<ObservedConnection, String> {
        self.metrics.acquire(&self.pool).await.map_err(storage)
    }

    /// `(topic, pending, oldest pending age in seconds)` of unpublished rows.
    pub async fn backlog(&self) -> Result<Vec<(String, u64, f64)>, String> {
        let rows = sqlx::query(
            "SELECT topic, count(*) AS pending,
                 EXTRACT(EPOCH FROM now() - min(occurred_at))::float8 AS age
             FROM delivery_outbox WHERE published_at IS NULL GROUP BY topic",
        )
        .fetch_all(&mut *self.connection().await?)
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
        .execute(&mut *self.connection().await?)
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
        .fetch_all(&mut *self.connection().await?)
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
            .execute(&mut *self.connection().await?)
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
        .execute(&mut *self.connection().await?)
        .await
        .map(drop)
        .map_err(storage)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn lock_key_splits_into_pg_locks_columns() {
        assert_eq!(
            advisory_lock_parts(RELAY_LOCK_KEY),
            (0x646c_7672, 0x656c_6179)
        );
        assert_eq!(advisory_lock_parts(-1), (u32::MAX, u32::MAX));
        assert_eq!(advisory_lock_parts(1), (0, 1));
        assert_eq!(advisory_lock_parts(i64::MIN), (0x8000_0000, 0));
    }

    #[test]
    fn lock_connection_bounds_statements_and_keeps_alive() {
        let options = lock_connect_options("postgres://u:p@localhost:5432/db").expect("options");
        let startup = options.get_options().expect("startup options");
        for name in [
            "statement_timeout",
            "tcp_keepalives_idle",
            "tcp_keepalives_interval",
            "tcp_keepalives_count",
            "tcp_user_timeout",
        ] {
            assert!(startup.contains(name), "{name} in {startup}");
        }
    }
}

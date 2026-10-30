//! Delivery Duration Statistics (FR-676) and the Transactional Inbox of the
//! consumers (ADR 11) of [`Store`]. Inside a Unit of Work the Inbox record
//! and the statistics change commit together.

use std::time::Duration;

use crate::pkg::persistence::RepositoryError;
use crate::slices::courier::domains::courier::MarketId;
use crate::slices::delivery::applications::duration::estimate_delivery_duration::DeliveryDurationSamples;
use crate::slices::delivery::applications::duration::record_delivery_duration::{
    DeliveryDurationStatistics, DeliveryInbox, OpenTransit,
};
use crate::slices::delivery::domains::delivery::{DeliveryId, DurationKind};

use super::rows::market_id;
use super::{Store, corrupt, storage_error};

impl DeliveryInbox for Store {
    /// Retention as in the Outbox: the claim deletes Inbox rows older than
    /// seven days in the same statement, `SKIP LOCKED` so concurrent claims
    /// neither wait for nor deadlock on each other's deletes.
    fn claim(
        &self,
        consumer: &str,
        event_id: &str,
        event_type: &str,
    ) -> Result<bool, RepositoryError> {
        let claimed = sql!(self, "INSERT", |connection| sqlx::query!(
            "WITH expired AS (
                 DELETE FROM delivery_inbox WHERE (consumer, event_id) IN (
                     SELECT consumer, event_id FROM delivery_inbox
                     WHERE processed_at < now() - interval '7 days'
                     FOR UPDATE SKIP LOCKED))
             INSERT INTO delivery_inbox (consumer, event_id, event_type)
             VALUES ($1, $2, $3)
             ON CONFLICT (consumer, event_id) DO NOTHING",
            consumer,
            event_id,
            event_type
        )
        .execute(connection))?;
        Ok(claimed.rows_affected() == 1)
    }
}

impl DeliveryDurationStatistics for Store {
    fn open_transit(
        &self,
        delivery_id: &DeliveryId,
        market: &MarketId,
        picked_up_at_ms: i64,
    ) -> Result<(), RepositoryError> {
        sql!(self, "INSERT", |connection| sqlx::query!(
            "INSERT INTO delivery_open_transits (delivery_id, market_id, picked_up_at_ms)
             VALUES ($1, $2, $3)
             ON CONFLICT (delivery_id) DO NOTHING",
            delivery_id.as_str(),
            market.as_str(),
            picked_up_at_ms
        )
        .execute(connection))
        .map(drop)
    }

    fn close_transit(
        &self,
        delivery_id: &DeliveryId,
    ) -> Result<Option<OpenTransit>, RepositoryError> {
        sql!(self, "DELETE", |connection| sqlx::query!(
            "DELETE FROM delivery_open_transits WHERE delivery_id = $1
             RETURNING market_id, picked_up_at_ms",
            delivery_id.as_str()
        )
        .fetch_optional(connection))?
        .map(|row| {
            Ok(OpenTransit {
                market_id: market_id(&row.market_id)?,
                picked_up_at_ms: row.picked_up_at_ms,
            })
        })
        .transpose()
    }

    fn record_duration(
        &self,
        market: &MarketId,
        kind: DurationKind,
        delivery_id: &DeliveryId,
        duration: Duration,
        keep: u32,
    ) -> Result<(), RepositoryError> {
        let duration_ms = i64::try_from(duration.as_millis())
            .map_err(|_| corrupt("duration", duration.as_millis()))?;
        let inserted = sql!(self, "INSERT", |connection| sqlx::query!(
            "INSERT INTO delivery_duration_samples (market_id, kind, delivery_id, duration_ms)
             VALUES ($1, $2, $3, $4)
             ON CONFLICT (kind, delivery_id) DO NOTHING",
            market.as_str(),
            kind.as_str(),
            delivery_id.as_str(),
            duration_ms
        )
        .execute(connection))?;
        if inserted.rows_affected() == 0 {
            return Ok(());
        }
        sql!(self, "DELETE", |connection| sqlx::query!(
            "DELETE FROM delivery_duration_samples
             WHERE market_id = $1 AND kind = $2 AND position NOT IN (
                 SELECT position FROM delivery_duration_samples
                 WHERE market_id = $1 AND kind = $2
                 ORDER BY position DESC LIMIT $3)",
            market.as_str(),
            kind.as_str(),
            i64::from(keep)
        )
        .execute(connection))
        .map(drop)
    }
}

impl DeliveryDurationSamples for Store {
    fn latest_durations(
        &self,
        market: &MarketId,
        kind: DurationKind,
        limit: u32,
    ) -> Result<Vec<Duration>, RepositoryError> {
        sql!(self, "SELECT", |connection| sqlx::query_scalar!(
            "SELECT duration_ms FROM delivery_duration_samples
             WHERE market_id = $1 AND kind = $2
             ORDER BY position DESC LIMIT $3",
            market.as_str(),
            kind.as_str(),
            i64::from(limit)
        )
        .fetch_all(connection))?
        .into_iter()
        .map(|duration_ms| {
            u64::try_from(duration_ms)
                .map(Duration::from_millis)
                .map_err(|_| corrupt("delivery duration sample", duration_ms))
        })
        .collect()
    }
}

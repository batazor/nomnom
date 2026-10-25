//! Transactional Outbox of [`PostgresStore`] (ADR 13, ADR 31): the Domain
//! Event → Integration Event mapper and the stable `event_id`. Debezium
//! publishes the inserted rows from the WAL (ADR 37): a row carries every
//! field of the Kafka message, and each Outbox write deletes rows older than
//! an hour in the same statement.

use uuid::Uuid;

use crate::pkg::persistence::RepositoryError;
use crate::pkg::telemetry::current_traceparent;
use crate::slices::delivery::applications::outbox::{DeliveryOutbox, OutboxMessage};

use super::{PostgresStore, storage_error};

/// Kafka topic of `delivery` Integration Events (ADR 31).
pub const OUTBOX_TOPIC: &str = "delivery.v1.delivery-events";

/// Namespace of `event_id` UUIDv5 (ADR 31).
const EVENT_ID_NAMESPACE: Uuid = Uuid::from_u128(0x8f1d_3c2a_5b7e_4a90_9c61_2e4f_7b8d_0a13);

/// `event_id` = UUIDv5 of `"{aggregate_type}:{aggregate_id}:{aggregate_version}:{event_type}"`.
#[must_use]
pub fn event_id(
    aggregate_type: &str,
    aggregate_id: &str,
    aggregate_version: i64,
    event_type: &str,
) -> Uuid {
    Uuid::new_v5(
        &EVENT_ID_NAMESPACE,
        format!("{aggregate_type}:{aggregate_id}:{aggregate_version}:{event_type}").as_bytes(),
    )
}

/// Integration Event of an Outbox row (ADR 31).
#[derive(Clone, Debug, PartialEq)]
pub struct OutboxRecord {
    /// Full Protobuf message name, e.g. `nomnom.delivery.v1.DeliveryFailed`.
    pub event_type: String,
    /// Protobuf payload of the contract.
    pub payload: Vec<u8>,
}

/// Mapper Domain Event → Integration Event. `None` — the event has no
/// contract and is not written to the Outbox.
pub trait OutboxMapper: Send + Sync {
    fn map(&self, message: &OutboxMessage) -> Option<OutboxRecord>;
}

/// Default mapper until the Protobuf mappers land: nothing is published.
pub struct NoIntegrationEvents;

impl OutboxMapper for NoIntegrationEvents {
    fn map(&self, _message: &OutboxMessage) -> Option<OutboxRecord> {
        None
    }
}

/// `traceparent` column of a row: the current span (the gRPC server span of
/// the command), `''` without one; Debezium sends it as the `traceparent`
/// header. The process consumes no Integration Events, so there is no
/// `causation_id`.
pub(super) fn row_traceparent() -> String {
    current_traceparent().unwrap_or_default()
}

/// Retention of the Outbox (ADR 37) as a CTE `expired` of every Outbox
/// write: rows older than an hour go in the transaction of the next write,
/// without a background job; the replication slot keeps the WAL of rows
/// Debezium has not read yet, and the publication ignores deletes. `SKIP
/// LOCKED`: concurrent writes neither wait for nor deadlock on each other's
/// deletes.
macro_rules! expire_outbox {
    () => {
        "expired AS (
            DELETE FROM delivery_outbox WHERE event_id IN (
                SELECT event_id FROM delivery_outbox
                WHERE created_at < now() - interval '1 hour'
                FOR UPDATE SKIP LOCKED))"
    };
}

/// Kafka topic of Courier Change Notifications (ADR 36): the BFF Router turns
/// them into `courierChanged` subscription events. Not an Integration Event:
/// no bounded context consumes it.
pub const COURIER_CHANGES_TOPIC: &str = "delivery.v1.courier-changes";

/// SQL tail that writes the Change Notification of every courier id a CTE
/// `changed (id)` returns: a write, its notification and the Outbox
/// retention are one statement, atomic with or without a Unit of Work. `$1`
/// is the [`row_traceparent`].
pub(super) const NOTIFY_CHANGED_COURIERS: &str = concat!(
    ", ",
    expire_outbox!(),
    "
    INSERT INTO delivery_outbox (event_id, event_type, aggregate_type, aggregate_id,
        aggregate_version, topic, payload, traceparent)
    SELECT gen_random_uuid(), 'CourierChanged', 'Courier', id, 0,
        'delivery.v1.courier-changes',
        convert_to(json_build_object('__typename', 'Courier', 'id', id)::text, 'UTF8'),
        $1
    FROM changed"
);

impl PostgresStore {
    /// Writes the Change Notification of `Courier(courier_id)` next to the
    /// change: only the GraphQL entity key, the Router re-reads the courier
    /// view with the subscriber's rights. Inside a Unit of Work it commits
    /// with the change. The key of every row is unique, so `event_id` is a
    /// UUIDv7 rather than the aggregate-version UUIDv5 of Integration Events.
    pub(super) fn notify_courier(&self, courier_id: &str) -> Result<(), RepositoryError> {
        let payload = serde_json::json!({ "__typename": "Courier", "id": courier_id }).to_string();
        sql!(self, "INSERT", |connection| sqlx::query(concat!(
            "WITH ",
            expire_outbox!(),
            "
             INSERT INTO delivery_outbox (event_id, event_type, aggregate_type, aggregate_id,
                 aggregate_version, topic, payload, traceparent)
             VALUES ($1::uuid, 'CourierChanged', 'Courier', $2, 0, $3, $4, $5)"
        ))
        .bind(Uuid::now_v7().to_string())
        .bind(courier_id)
        .bind(COURIER_CHANGES_TOPIC)
        .bind(payload.into_bytes())
        .bind(row_traceparent())
        .execute(connection))
        .map(drop)
    }
}

impl DeliveryOutbox for PostgresStore {
    /// Inside a Unit of Work the row commits or rolls back with the aggregate;
    /// `aggregate_version` is the version the same transaction just wrote.
    /// Events the mapper does not publish are not written.
    fn add(&self, message: OutboxMessage) -> Result<(), RepositoryError> {
        let Some(record) = self.mapper.map(&message) else {
            return Ok(());
        };
        let aggregate_id = message.aggregate_id.as_str();
        let version: Option<i64> = sql!(self, "SELECT", |connection| sqlx::query_scalar(
            "SELECT version FROM deliveries WHERE id = $1"
        )
        .bind(aggregate_id)
        .fetch_optional(connection))?;
        let version = version.ok_or_else(|| {
            RepositoryError::Unavailable(format!(
                "outbox event {} for unknown delivery {aggregate_id}",
                record.event_type
            ))
        })?;
        let id = event_id(
            message.aggregate_type,
            aggregate_id,
            version,
            &record.event_type,
        )
        .to_string();
        sql!(self, "INSERT", |connection| sqlx::query(concat!(
            "WITH ",
            expire_outbox!(),
            "
             INSERT INTO delivery_outbox (event_id, event_type, aggregate_type, aggregate_id,
                 aggregate_version, topic, payload, traceparent)
             VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8)"
        ))
        .bind(&id)
        .bind(&record.event_type)
        .bind(message.aggregate_type)
        .bind(aggregate_id)
        .bind(version)
        .bind(OUTBOX_TOPIC)
        .bind(&record.payload)
        .bind(row_traceparent())
        .execute(connection))
        .map(drop)
    }
}

#[cfg(test)]
mod tests {
    use opentelemetry::Context;
    use opentelemetry::trace::{
        SpanContext, SpanId, TraceContextExt as _, TraceFlags, TraceId, TraceState,
    };

    use super::*;

    #[test]
    fn traceparent_without_a_span_is_empty() {
        assert_eq!(row_traceparent(), "");
    }

    #[test]
    fn traceparent_is_the_current_span() {
        let span = SpanContext::new(
            TraceId::from_hex("4bf92f3577b34da6a3ce929d0e0e4736").expect("trace id"),
            SpanId::from_hex("00f067aa0ba902b7").expect("span id"),
            TraceFlags::SAMPLED,
            false,
            TraceState::default(),
        );
        let _guard = Context::new().with_remote_span_context(span).attach();
        assert_eq!(
            row_traceparent(),
            "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
        );
    }
}

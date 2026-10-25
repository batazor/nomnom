//! Transactional Outbox of [`PostgresStore`] (ADR 13, ADR 31): the Domain
//! Event → Integration Event mapper and the stable `event_id`.

use uuid::Uuid;

use crate::pkg::persistence::RepositoryError;
use crate::pkg::telemetry::{TRACEPARENT, current_traceparent};
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
    /// `traceparent`, `causation_id`.
    pub metadata: serde_json::Value,
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

/// Adds the `traceparent` of the current span (the gRPC server span of the
/// command) to the row metadata; the relay sends it as a Kafka header. The
/// process consumes no Integration Events, so there is no `causation_id`.
fn with_trace_context(metadata: serde_json::Value) -> serde_json::Value {
    let Some(traceparent) = current_traceparent() else {
        return metadata;
    };
    let mut metadata = match metadata {
        serde_json::Value::Object(fields) => fields,
        _ => serde_json::Map::new(),
    };
    metadata
        .entry(TRACEPARENT)
        .or_insert(serde_json::Value::String(traceparent));
    serde_json::Value::Object(metadata)
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
        let metadata = with_trace_context(record.metadata).to_string();
        sql!(self, "INSERT", |connection| sqlx::query(
            "INSERT INTO delivery_outbox (event_id, event_type, aggregate_type, aggregate_id,
                 aggregate_version, topic, payload, metadata)
             VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8::jsonb)"
        )
        .bind(&id)
        .bind(&record.event_type)
        .bind(message.aggregate_type)
        .bind(aggregate_id)
        .bind(version)
        .bind(OUTBOX_TOPIC)
        .bind(&record.payload)
        .bind(&metadata)
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
    fn metadata_without_a_span_is_unchanged() {
        assert_eq!(
            with_trace_context(serde_json::json!({})),
            serde_json::json!({})
        );
    }

    #[test]
    fn metadata_carries_the_current_traceparent() {
        let span = SpanContext::new(
            TraceId::from_hex("4bf92f3577b34da6a3ce929d0e0e4736").expect("trace id"),
            SpanId::from_hex("00f067aa0ba902b7").expect("span id"),
            TraceFlags::SAMPLED,
            false,
            TraceState::default(),
        );
        let _guard = Context::new().with_remote_span_context(span).attach();
        assert_eq!(
            with_trace_context(serde_json::json!({"causation_id": "c-1"})),
            serde_json::json!({
                "causation_id": "c-1",
                "traceparent": "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
            })
        );
    }
}

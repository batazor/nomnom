//! Transactional Outbox of [`PostgresStore`] (ADR 13, ADR 31): the Domain
//! Event → Integration Event mapper and the stable `event_id`.

use uuid::Uuid;

use crate::pkg::persistence::RepositoryError;
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

impl DeliveryOutbox for PostgresStore {
    /// Inside a Unit of Work the row commits or rolls back with the aggregate;
    /// `aggregate_version` is the version the same transaction just wrote.
    /// Events the mapper does not publish are not written.
    fn add(&self, message: OutboxMessage) -> Result<(), RepositoryError> {
        let Some(record) = self.mapper.map(&message) else {
            return Ok(());
        };
        let aggregate_id = message.aggregate_id.as_str();
        let version: Option<i64> = sql!(self, |connection| sqlx::query_scalar(
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
        let metadata = record.metadata.to_string();
        sql!(self, |connection| sqlx::query(
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

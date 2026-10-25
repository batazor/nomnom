use crate::pkg::persistence::RepositoryError;
use crate::slices::delivery::domains::delivery::{DeliveryId, DomainEvent};

pub const DELIVERY_AGGREGATE_TYPE: &str = "delivery";

/// Business fact handed to the Outbox inside the Unit of Work. The
/// infrastructure adapter adds `aggregate_version`, `event_id`, the Event
/// Envelope and the Integration Event mapping.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct OutboxMessage {
    pub aggregate_type: &'static str,
    pub aggregate_id: DeliveryId,
    pub event: DomainEvent,
}

impl OutboxMessage {
    #[must_use]
    pub fn delivery(event: DomainEvent) -> Self {
        Self {
            aggregate_type: DELIVERY_AGGREGATE_TYPE,
            aggregate_id: event.delivery_id().clone(),
            event,
        }
    }
}

/// Transactional Outbox port of the `delivery` slice. Writes participate in
/// the active Unit of Work.
pub trait DeliveryOutbox {
    fn add(&self, message: OutboxMessage) -> Result<(), RepositoryError>;
}

pub(crate) fn publish(
    outbox: &dyn DeliveryOutbox,
    events: Vec<DomainEvent>,
) -> Result<(), RepositoryError> {
    events
        .into_iter()
        .try_for_each(|event| outbox.add(OutboxMessage::delivery(event)))
}

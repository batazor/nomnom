//! Consumer `delivery.duration-statistics` of the Delivery's own facts
//! (`delivery.v1.delivery-events`, FR-676): the Integration Event → Record
//! Delivery Duration mapping and the class of its failures.
//!
//! | Event | Fact |
//! | --- | --- |
//! | `DeliveryPickedUp` | Pickup |
//! | `DeliveryCompleted` | handover |
//! | `DeliveryCancelled`, `DeliveryFailed` | leaves without a duration |
//! | others | not consumed, committed without processing |

use prost::Message;

use crate::infrastructure::grpc::v1;
use crate::infrastructure::messaging::{Envelope, HandlingError};
use crate::slices::delivery::applications::error::DeliveryApplicationError;
use crate::slices::delivery::applications::record_delivery_duration::{Fact, Input};

use super::integration_events::{
    DELIVERY_CANCELLED, DELIVERY_COMPLETED, DELIVERY_FAILED, DELIVERY_PICKED_UP,
};

/// Record Delivery Duration of a message; `None` — the type is not consumed.
pub fn duration_input(envelope: &Envelope, payload: &[u8]) -> Result<Option<Input>, HandlingError> {
    let decoded =
        match envelope.event_type.as_str() {
            DELIVERY_PICKED_UP => v1::DeliveryPickedUp::decode(payload)
                .map(|event| (event.delivery_id, Fact::PickedUp)),
            DELIVERY_COMPLETED => v1::DeliveryCompleted::decode(payload)
                .map(|event| (event.delivery_id, Fact::Completed)),
            DELIVERY_CANCELLED => v1::DeliveryCancelled::decode(payload)
                .map(|event| (event.delivery_id, Fact::Dropped)),
            DELIVERY_FAILED => {
                v1::DeliveryFailed::decode(payload).map(|event| (event.delivery_id, Fact::Dropped))
            }
            _ => return Ok(None),
        };
    let (delivery_id, fact) = decoded.map_err(|error| {
        HandlingError::Permanent(format!(
            "deserialization of {}: {error}",
            envelope.event_type
        ))
    })?;
    Ok(Some(Input {
        event_id: envelope.event_id.clone(),
        event_type: envelope.event_type.clone(),
        occurred_at_ms: envelope.occurred_at_ms,
        delivery_id,
        fact,
    }))
}

/// Class of a failed Record Delivery Duration: a storage outage or a lost
/// race is repeated, anything else is a defect of the event or the code.
#[must_use]
pub fn handling_error(error: &DeliveryApplicationError) -> HandlingError {
    match error {
        DeliveryApplicationError::DependencyUnavailable(_)
        | DeliveryApplicationError::Transaction(_)
        | DeliveryApplicationError::ConcurrentModification => {
            HandlingError::Transient(error.to_string())
        }
        DeliveryApplicationError::PermissionDenied
        | DeliveryApplicationError::InvalidRequest(_)
        | DeliveryApplicationError::DeliveryNotFound(_)
        | DeliveryApplicationError::DeliveryForOrderNotFound(_)
        | DeliveryApplicationError::DeliveryForOrderInOtherMarket { .. }
        | DeliveryApplicationError::CourierNotFound(_)
        | DeliveryApplicationError::DeliveryOfferNotFound(_)
        | DeliveryApplicationError::DeliveryPrecondition(_)
        | DeliveryApplicationError::CourierPrecondition(_)
        | DeliveryApplicationError::OfferPrecondition(_)
        | DeliveryApplicationError::CourierPositionUnknown(_)
        | DeliveryApplicationError::Internal(_) => HandlingError::Permanent(error.to_string()),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::pkg::unit_of_work::UnitOfWorkError;
    use crate::slices::delivery::domains::delivery::DeliveryId;

    use super::super::integration_events::COURIER_ASSIGNED_TO_DELIVERY;

    fn envelope(event_type: &str) -> Envelope {
        Envelope {
            event_id: "event-1".to_owned(),
            event_type: event_type.to_owned(),
            occurred_at_ms: 1_000,
        }
    }

    #[test]
    fn maps_the_facts_of_the_statistics() {
        for (event_type, payload, fact) in [
            (
                DELIVERY_PICKED_UP,
                v1::DeliveryPickedUp {
                    delivery_id: "delivery-1".to_owned(),
                    ..v1::DeliveryPickedUp::default()
                }
                .encode_to_vec(),
                Fact::PickedUp,
            ),
            (
                DELIVERY_COMPLETED,
                v1::DeliveryCompleted {
                    delivery_id: "delivery-1".to_owned(),
                    ..v1::DeliveryCompleted::default()
                }
                .encode_to_vec(),
                Fact::Completed,
            ),
            (
                DELIVERY_CANCELLED,
                v1::DeliveryCancelled {
                    delivery_id: "delivery-1".to_owned(),
                    ..v1::DeliveryCancelled::default()
                }
                .encode_to_vec(),
                Fact::Dropped,
            ),
            (
                DELIVERY_FAILED,
                v1::DeliveryFailed {
                    delivery_id: "delivery-1".to_owned(),
                    ..v1::DeliveryFailed::default()
                }
                .encode_to_vec(),
                Fact::Dropped,
            ),
        ] {
            assert_eq!(
                duration_input(&envelope(event_type), &payload),
                Ok(Some(Input {
                    event_id: "event-1".to_owned(),
                    event_type: event_type.to_owned(),
                    occurred_at_ms: 1_000,
                    delivery_id: "delivery-1".to_owned(),
                    fact,
                })),
                "{event_type}"
            );
        }
    }

    #[test]
    fn other_types_are_not_consumed() {
        assert_eq!(
            duration_input(&envelope(COURIER_ASSIGNED_TO_DELIVERY), b"anything"),
            Ok(None)
        );
        assert_eq!(
            duration_input(&envelope("nomnom.orders.v1.OrderPlaced"), b""),
            Ok(None)
        );
    }

    #[test]
    fn undecodable_payload_is_permanent() {
        assert!(matches!(
            duration_input(&envelope(DELIVERY_PICKED_UP), &[0xff, 0xff, 0xff]),
            Err(HandlingError::Permanent(_))
        ));
    }

    #[test]
    fn storage_failures_are_transient_and_defects_permanent() {
        assert!(matches!(
            handling_error(&DeliveryApplicationError::DependencyUnavailable(
                "db down".to_owned()
            )),
            HandlingError::Transient(_)
        ));
        assert!(matches!(
            handling_error(&DeliveryApplicationError::Transaction(
                UnitOfWorkError::CommitOutcomeUnknown
            )),
            HandlingError::Transient(_)
        ));
        assert!(matches!(
            handling_error(&DeliveryApplicationError::DeliveryNotFound(
                DeliveryId::new("delivery-1")
            )),
            HandlingError::Permanent(_)
        ));
    }
}

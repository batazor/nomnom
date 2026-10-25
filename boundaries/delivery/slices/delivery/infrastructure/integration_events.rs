//! Domain Event → Integration Event of `nomnom/delivery/v1/events.proto`
//! (ADR 25, ADR 31). Published types are an explicit allowlist: every Domain
//! Event of the Delivery aggregate has a contract message; a new variant fails
//! to compile until it is mapped or explicitly excluded.

use prost::Message;

use crate::infrastructure::grpc::v1;
use crate::infrastructure::postgres::{OutboxMapper, OutboxRecord};
use crate::slices::delivery::applications::outbox::OutboxMessage;
use crate::slices::delivery::domains::delivery::DomainEvent;

/// `event_type` — full Protobuf message names of the contract.
pub const COURIER_ASSIGNED_TO_DELIVERY: &str = "nomnom.delivery.v1.CourierAssignedToDelivery";
pub const DELIVERY_PICKED_UP: &str = "nomnom.delivery.v1.DeliveryPickedUp";
pub const DELIVERY_COMPLETED: &str = "nomnom.delivery.v1.DeliveryCompleted";
pub const DELIVERY_FAILED: &str = "nomnom.delivery.v1.DeliveryFailed";
pub const COURIER_UNASSIGNED_FROM_DELIVERY: &str =
    "nomnom.delivery.v1.CourierUnassignedFromDelivery";
pub const DELIVERY_CANCELLED: &str = "nomnom.delivery.v1.DeliveryCancelled";

/// Mapper of the `delivery.v1.delivery-events` stream.
#[derive(Clone, Copy, Debug, Default)]
pub struct DeliveryIntegrationEvents;

impl OutboxMapper for DeliveryIntegrationEvents {
    fn map(&self, message: &OutboxMessage) -> Option<OutboxRecord> {
        let (event_type, payload) = match &message.event {
            DomainEvent::CourierAssignedToDelivery {
                delivery_id,
                order_id,
                courier_id,
            } => (
                COURIER_ASSIGNED_TO_DELIVERY,
                v1::CourierAssignedToDelivery {
                    order_id: order_id.as_str().to_owned(),
                    delivery_id: delivery_id.as_str().to_owned(),
                    courier_id: courier_id.as_str().to_owned(),
                }
                .encode_to_vec(),
            ),
            DomainEvent::DeliveryPickedUp {
                delivery_id,
                order_id,
                ..
            } => (
                DELIVERY_PICKED_UP,
                v1::DeliveryPickedUp {
                    order_id: order_id.as_str().to_owned(),
                    delivery_id: delivery_id.as_str().to_owned(),
                }
                .encode_to_vec(),
            ),
            DomainEvent::DeliveryCompleted {
                delivery_id,
                order_id,
                ..
            } => (
                DELIVERY_COMPLETED,
                v1::DeliveryCompleted {
                    order_id: order_id.as_str().to_owned(),
                    delivery_id: delivery_id.as_str().to_owned(),
                }
                .encode_to_vec(),
            ),
            DomainEvent::DeliveryFailed {
                delivery_id,
                order_id,
                reason,
                ..
            } => (
                DELIVERY_FAILED,
                v1::DeliveryFailed {
                    order_id: order_id.as_str().to_owned(),
                    delivery_id: delivery_id.as_str().to_owned(),
                    reason: reason.as_str().to_owned(),
                }
                .encode_to_vec(),
            ),
            DomainEvent::CourierUnassignedFromDelivery {
                delivery_id,
                order_id,
                courier_id,
            } => (
                COURIER_UNASSIGNED_FROM_DELIVERY,
                v1::CourierUnassignedFromDelivery {
                    order_id: order_id.as_str().to_owned(),
                    delivery_id: delivery_id.as_str().to_owned(),
                    courier_id: courier_id.as_str().to_owned(),
                }
                .encode_to_vec(),
            ),
            DomainEvent::DeliveryCancelled {
                delivery_id,
                order_id,
                reason,
                ..
            } => (
                DELIVERY_CANCELLED,
                v1::DeliveryCancelled {
                    order_id: order_id.as_str().to_owned(),
                    delivery_id: delivery_id.as_str().to_owned(),
                    reason: reason
                        .as_ref()
                        .map(|reason| reason.as_str().to_owned())
                        .unwrap_or_default(),
                }
                .encode_to_vec(),
            ),
        };
        Some(OutboxRecord {
            event_type: event_type.to_owned(),
            payload,
            // The process has no trace context yet; the relay sends traceparent
            // when the row carries one.
            metadata: serde_json::json!({}),
        })
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::slices::courier::domains::courier::CourierId;
    use crate::slices::delivery::domains::delivery::{
        DeliveryCancellationReason, DeliveryFailureReason, DeliveryId, OrderId,
    };

    fn ids() -> (DeliveryId, OrderId, CourierId) {
        (
            DeliveryId::new("delivery-1"),
            OrderId::new("order-1"),
            CourierId::new("courier-1"),
        )
    }

    fn map(event: DomainEvent) -> Option<OutboxRecord> {
        DeliveryIntegrationEvents.map(&OutboxMessage::delivery(event))
    }

    #[test]
    fn maps_courier_assignment() {
        let (delivery_id, order_id, courier_id) = ids();
        let record = map(DomainEvent::CourierAssignedToDelivery {
            delivery_id,
            order_id,
            courier_id,
        })
        .expect("published");
        assert_eq!(record.event_type, COURIER_ASSIGNED_TO_DELIVERY);
        assert_eq!(
            v1::CourierAssignedToDelivery::decode(record.payload.as_slice()).expect("decode"),
            v1::CourierAssignedToDelivery {
                order_id: "order-1".to_owned(),
                delivery_id: "delivery-1".to_owned(),
                courier_id: "courier-1".to_owned(),
            }
        );
        assert_eq!(record.metadata, serde_json::json!({}));
    }

    #[test]
    fn maps_pickup_and_completion() {
        let (delivery_id, order_id, courier_id) = ids();
        let picked = map(DomainEvent::DeliveryPickedUp {
            delivery_id: delivery_id.clone(),
            order_id: order_id.clone(),
            courier_id: courier_id.clone(),
        })
        .expect("published");
        assert_eq!(picked.event_type, DELIVERY_PICKED_UP);
        assert_eq!(
            v1::DeliveryPickedUp::decode(picked.payload.as_slice()).expect("decode"),
            v1::DeliveryPickedUp {
                order_id: "order-1".to_owned(),
                delivery_id: "delivery-1".to_owned(),
            }
        );

        let completed = map(DomainEvent::DeliveryCompleted {
            delivery_id,
            order_id,
            courier_id,
        })
        .expect("published");
        assert_eq!(completed.event_type, DELIVERY_COMPLETED);
        assert_eq!(
            v1::DeliveryCompleted::decode(completed.payload.as_slice()).expect("decode"),
            v1::DeliveryCompleted {
                order_id: "order-1".to_owned(),
                delivery_id: "delivery-1".to_owned(),
            }
        );
    }

    #[test]
    fn maps_failure_with_reason() {
        let (delivery_id, order_id, courier_id) = ids();
        let record = map(DomainEvent::DeliveryFailed {
            delivery_id,
            order_id,
            courier_id,
            reason: DeliveryFailureReason::new("customer absent").expect("reason"),
        })
        .expect("published");
        assert_eq!(record.event_type, DELIVERY_FAILED);
        assert_eq!(
            v1::DeliveryFailed::decode(record.payload.as_slice()).expect("decode"),
            v1::DeliveryFailed {
                order_id: "order-1".to_owned(),
                delivery_id: "delivery-1".to_owned(),
                reason: "customer absent".to_owned(),
            }
        );
    }

    #[test]
    fn maps_courier_unassignment() {
        let (delivery_id, order_id, courier_id) = ids();
        let record = map(DomainEvent::CourierUnassignedFromDelivery {
            delivery_id,
            order_id,
            courier_id,
        })
        .expect("published");
        assert_eq!(record.event_type, COURIER_UNASSIGNED_FROM_DELIVERY);
        assert_eq!(
            v1::CourierUnassignedFromDelivery::decode(record.payload.as_slice()).expect("decode"),
            v1::CourierUnassignedFromDelivery {
                order_id: "order-1".to_owned(),
                delivery_id: "delivery-1".to_owned(),
                courier_id: "courier-1".to_owned(),
            }
        );
    }

    #[test]
    fn maps_cancellation_with_and_without_reason() {
        let (delivery_id, order_id, courier_id) = ids();
        let record = map(DomainEvent::DeliveryCancelled {
            delivery_id: delivery_id.clone(),
            order_id: order_id.clone(),
            released_courier_id: Some(courier_id),
            reason: DeliveryCancellationReason::new("customer request"),
        })
        .expect("published");
        assert_eq!(record.event_type, DELIVERY_CANCELLED);
        assert_eq!(
            v1::DeliveryCancelled::decode(record.payload.as_slice()).expect("decode"),
            v1::DeliveryCancelled {
                order_id: "order-1".to_owned(),
                delivery_id: "delivery-1".to_owned(),
                reason: "customer request".to_owned(),
            }
        );

        let without_reason = map(DomainEvent::DeliveryCancelled {
            delivery_id,
            order_id,
            released_courier_id: None,
            reason: None,
        })
        .expect("published");
        assert_eq!(
            v1::DeliveryCancelled::decode(without_reason.payload.as_slice()).expect("decode"),
            v1::DeliveryCancelled {
                order_id: "order-1".to_owned(),
                delivery_id: "delivery-1".to_owned(),
                reason: String::new(),
            }
        );
    }

    /// ADR 31: two events of one transaction share the aggregate version, so
    /// `event_type` keeps their ids apart (unassignment followed by a new
    /// assignment on the same version would not collide).
    #[test]
    fn unassignment_and_assignment_on_one_version_have_distinct_ids() {
        let unassigned = crate::infrastructure::postgres::event_id(
            "delivery",
            "delivery-1",
            5,
            COURIER_UNASSIGNED_FROM_DELIVERY,
        );
        let assigned = crate::infrastructure::postgres::event_id(
            "delivery",
            "delivery-1",
            5,
            COURIER_ASSIGNED_TO_DELIVERY,
        );
        assert_ne!(unassigned, assigned);
        assert_ne!(
            crate::infrastructure::postgres::event_id(
                "delivery",
                "delivery-1",
                5,
                DELIVERY_CANCELLED
            ),
            unassigned
        );
    }

    /// Reference value computed independently (Python `uuid.uuid5`) with the
    /// ADR 31 namespace `8f1d3c2a-5b7e-4a90-9c61-2e4f7b8d0a13`.
    #[test]
    fn event_id_is_uuid_v5_of_envelope_key() {
        let id = crate::infrastructure::postgres::event_id(
            "delivery",
            "delivery-1",
            3,
            DELIVERY_PICKED_UP,
        );
        assert_eq!(id.to_string(), "7bab3765-b089-5ab8-8bcd-eaf2ea654b5a");
        assert_eq!(id.get_version_num(), 5);
        // Every envelope component changes the id.
        assert_ne!(
            id,
            crate::infrastructure::postgres::event_id(
                "delivery",
                "delivery-1",
                4,
                DELIVERY_PICKED_UP
            )
        );
        assert_ne!(
            id,
            crate::infrastructure::postgres::event_id(
                "delivery",
                "delivery-1",
                3,
                DELIVERY_COMPLETED
            )
        );
    }

    #[test]
    fn event_types_are_full_protobuf_names() {
        fn full_name<T>() -> String {
            format!(
                "nomnom.delivery.v1.{}",
                std::any::type_name::<T>()
                    .rsplit("::")
                    .next()
                    .expect("name")
            )
        }
        assert_eq!(
            COURIER_UNASSIGNED_FROM_DELIVERY,
            full_name::<v1::CourierUnassignedFromDelivery>()
        );
        assert_eq!(DELIVERY_CANCELLED, full_name::<v1::DeliveryCancelled>());
        assert_eq!(
            COURIER_ASSIGNED_TO_DELIVERY,
            format!(
                "nomnom.delivery.v1.{}",
                std::any::type_name::<v1::CourierAssignedToDelivery>()
                    .rsplit("::")
                    .next()
                    .expect("name")
            )
        );
    }
}

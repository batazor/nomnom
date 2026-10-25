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
mod tests;

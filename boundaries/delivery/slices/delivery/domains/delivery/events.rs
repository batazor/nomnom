use super::{DeliveryCancellationReason, DeliveryFailureReason, DeliveryId, OrderId};
use crate::slices::courier::domains::courier::CourierId;

#[derive(Clone, Debug, Eq, PartialEq)]
pub enum DomainEvent {
    CourierAssignedToDelivery {
        delivery_id: DeliveryId,
        order_id: OrderId,
        courier_id: CourierId,
    },
    CourierUnassignedFromDelivery {
        delivery_id: DeliveryId,
        order_id: OrderId,
        courier_id: CourierId,
    },
    DeliveryPickedUp {
        delivery_id: DeliveryId,
        order_id: OrderId,
        courier_id: CourierId,
    },
    DeliveryCompleted {
        delivery_id: DeliveryId,
        order_id: OrderId,
        courier_id: CourierId,
    },
    DeliveryFailed {
        delivery_id: DeliveryId,
        order_id: OrderId,
        courier_id: CourierId,
        reason: DeliveryFailureReason,
    },
    /// The Delivery was cancelled before Pickup; the courier assigned at that
    /// moment, if any, is released.
    DeliveryCancelled {
        delivery_id: DeliveryId,
        order_id: OrderId,
        released_courier_id: Option<CourierId>,
        reason: Option<DeliveryCancellationReason>,
    },
}

impl DomainEvent {
    #[must_use]
    pub fn delivery_id(&self) -> &DeliveryId {
        match self {
            Self::CourierAssignedToDelivery { delivery_id, .. }
            | Self::CourierUnassignedFromDelivery { delivery_id, .. }
            | Self::DeliveryPickedUp { delivery_id, .. }
            | Self::DeliveryCompleted { delivery_id, .. }
            | Self::DeliveryFailed { delivery_id, .. }
            | Self::DeliveryCancelled { delivery_id, .. } => delivery_id,
        }
    }
}

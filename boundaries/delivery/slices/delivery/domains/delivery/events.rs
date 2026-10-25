use super::{
    DeliveryCancellationReason, DeliveryFailureReason, DeliveryId, DeliveryOfferId, OrderId,
};
use crate::slices::courier::domains::courier::CourierId;

#[derive(Clone, Debug, Eq, PartialEq)]
pub enum DomainEvent {
    /// A new Delivery was created for an order (Create Delivery).
    DeliveryCreated {
        delivery_id: DeliveryId,
        order_id: OrderId,
        created_at_epoch_seconds: i64,
    },
    CourierAssignedToDelivery {
        delivery_id: DeliveryId,
        order_id: OrderId,
        courier_id: CourierId,
        /// Creation time of the Delivery; `None` for a Delivery stored
        /// before the creation time was recorded.
        created_at_epoch_seconds: Option<i64>,
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
        /// Creation time of the Delivery; `None` for a Delivery stored
        /// before the creation time was recorded.
        created_at_epoch_seconds: Option<i64>,
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
    /// The addressee accepted a Delivery Offer; the Courier Assignment is
    /// [`DomainEvent::CourierAssignedToDelivery`] of the same transaction.
    DeliveryOfferAccepted {
        delivery_id: DeliveryId,
        offer_id: DeliveryOfferId,
        courier_id: CourierId,
    },
    /// The addressee rejected a Delivery Offer.
    DeliveryOfferRejected {
        delivery_id: DeliveryId,
        offer_id: DeliveryOfferId,
        courier_id: CourierId,
    },
    /// A pending Delivery Offer was closed after its deadline without an
    /// answer (Expire Delivery Offers). Internal: not published.
    DeliveryOfferExpired {
        delivery_id: DeliveryId,
        offer_id: DeliveryOfferId,
        courier_id: CourierId,
    },
}

impl DomainEvent {
    #[must_use]
    pub fn delivery_id(&self) -> &DeliveryId {
        match self {
            Self::DeliveryCreated { delivery_id, .. }
            | Self::CourierAssignedToDelivery { delivery_id, .. }
            | Self::CourierUnassignedFromDelivery { delivery_id, .. }
            | Self::DeliveryPickedUp { delivery_id, .. }
            | Self::DeliveryCompleted { delivery_id, .. }
            | Self::DeliveryFailed { delivery_id, .. }
            | Self::DeliveryCancelled { delivery_id, .. }
            | Self::DeliveryOfferAccepted { delivery_id, .. }
            | Self::DeliveryOfferRejected { delivery_id, .. }
            | Self::DeliveryOfferExpired { delivery_id, .. } => delivery_id,
        }
    }
}

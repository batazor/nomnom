use crate::pkg::application::{AuthorizationError, Subject};
use crate::slices::courier::domains::courier::CourierId;
use crate::slices::delivery::domains::delivery::{DeliveryId, OrderId};

/// Permissions checked by the `delivery` slice use cases.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum DeliveryPermission {
    CreateDelivery,
    FindCourier,
    OfferDelivery,
    AcceptDeliveryOffer,
    RejectDeliveryOffer,
    ReassignCourier,
    ConfirmPickup,
    ConfirmDelivery,
    ReportDeliveryFailure,
    ListAvailableDeliveryOffers,
    CancelDelivery,
    ReplanDelivery,
    /// The name of the courier of an order, for whoever may view the order
    /// (FR-626).
    ViewOrderCourier,
}

/// Resource of a delivery permission check.
#[derive(Clone, Debug, Eq, PartialEq)]
pub enum DeliveryResource {
    /// Platform-wide operations such as requesting a new delivery.
    Platform,
    /// Operations on a concrete delivery: assignment process, dispatcher.
    Delivery(DeliveryId),
    /// Commands of the order fulfillment process addressed by the order: one
    /// Delivery exists per order.
    Order(OrderId),
    /// Operations performed on behalf of a concrete courier. Whether the
    /// offer or delivery belongs to that courier is a domain check.
    Courier(CourierId),
}

/// Authorization port of the `delivery` slice; the SpiceDB adapter lives in
/// `infrastructure/`.
pub trait DeliveryAuthorization {
    fn check(
        &self,
        subject: &Subject,
        permission: DeliveryPermission,
        resource: &DeliveryResource,
    ) -> Result<(), AuthorizationError>;
}

pub mod assignment;
pub mod assignment_process;
pub mod authorization;
pub mod courier_locator;
pub mod duration;
pub mod error;
pub mod execution;
pub mod offer;
pub mod offers_of_delivery;
pub mod outbox;
pub mod policy;
pub mod relationships;
pub mod tracking;

#[cfg(test)]
mod test_support;

use crate::pkg::usecase::{Clock, required_identifier};
use crate::slices::courier::domains::courier::{CourierId, MarketId};
use crate::slices::delivery::domains::delivery::{
    Delivery, DeliveryId, DeliveryOffer, DeliveryOfferId, DeliveryOfferRepository,
    DeliveryRepository, ExpectedTime, OrderId,
};

use error::{DeliveryApplicationError, InvalidDeliveryRequest};

fn identifier(value: &str, field: &'static str) -> Result<String, DeliveryApplicationError> {
    required_identifier(value)
        .map(str::to_owned)
        .ok_or(DeliveryApplicationError::InvalidRequest(
            InvalidDeliveryRequest::EmptyIdentifier(field),
        ))
}

fn delivery_id(value: &str) -> Result<DeliveryId, DeliveryApplicationError> {
    identifier(value, "delivery_id").map(DeliveryId::new)
}

fn courier_id(value: &str) -> Result<CourierId, DeliveryApplicationError> {
    identifier(value, "courier_id").map(CourierId::new)
}

fn offer_id(value: &str) -> Result<DeliveryOfferId, DeliveryApplicationError> {
    identifier(value, "offer_id").map(DeliveryOfferId::new)
}

fn order_id(value: &str) -> Result<OrderId, DeliveryApplicationError> {
    identifier(value, "order_id").map(OrderId::new)
}

/// Required `market_id` of a request (ADR 41): empty or not a slug is an
/// invalid request.
fn market_id(value: &str) -> Result<MarketId, DeliveryApplicationError> {
    MarketId::new(value).map_err(|error| {
        DeliveryApplicationError::InvalidRequest(InvalidDeliveryRequest::Market(error))
    })
}

/// Expected time named by the courier in `field`: in the future by `clock`.
fn expected_time(
    field: &'static str,
    epoch_seconds: Option<i64>,
    clock: &dyn Clock,
) -> Result<Option<ExpectedTime>, DeliveryApplicationError> {
    epoch_seconds
        .map(|at| ExpectedTime::named(field, at, clock.now_epoch_seconds()))
        .transpose()
        .map_err(DeliveryApplicationError::from)
}

fn load_delivery(
    deliveries: &dyn DeliveryRepository,
    id: &DeliveryId,
) -> Result<Delivery, DeliveryApplicationError> {
    deliveries
        .get(id)?
        .ok_or_else(|| DeliveryApplicationError::DeliveryNotFound(id.clone()))
}

fn load_delivery_by_order(
    deliveries: &dyn DeliveryRepository,
    order_id: &OrderId,
) -> Result<Delivery, DeliveryApplicationError> {
    deliveries
        .find_by_order(order_id)?
        .ok_or_else(|| DeliveryApplicationError::DeliveryForOrderNotFound(order_id.clone()))
}

fn load_offer(
    offers: &dyn DeliveryOfferRepository,
    id: &DeliveryOfferId,
) -> Result<DeliveryOffer, DeliveryApplicationError> {
    offers
        .get(id)?
        .ok_or_else(|| DeliveryApplicationError::DeliveryOfferNotFound(id.clone()))
}

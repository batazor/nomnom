pub mod assignment;
pub mod authorization;
pub mod error;
pub mod register_courier;
pub mod relationships;
pub mod retire_courier;
pub mod set_courier_availability;
pub mod update_courier_location;

use crate::pkg::application::required_identifier;
use crate::slices::courier::domains::courier::{CourierId, MarketId};

use error::{CourierApplicationError, InvalidCourierRequest};

fn courier_id(value: &str) -> Result<CourierId, CourierApplicationError> {
    required_identifier(value)
        .map(CourierId::new)
        .ok_or(CourierApplicationError::InvalidRequest(
            InvalidCourierRequest::EmptyCourierId,
        ))
}

/// Required `market_id` of a request (ADR 41).
fn market_id(value: &str) -> Result<MarketId, CourierApplicationError> {
    MarketId::new(value).map_err(|error| {
        CourierApplicationError::InvalidRequest(InvalidCourierRequest::Market(error))
    })
}

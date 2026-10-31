pub mod assignment;
pub mod authorization;
pub mod candidate_wake;
pub mod error;
pub mod register;
pub mod relationships;
pub mod retire;
pub mod set_availability;
pub mod update_location;

use crate::slices::courier::domains::courier::{CourierId, MarketId};

use error::{CourierApplicationError, InvalidCourierRequest};

/// Source of the current time for application code; domain code receives
/// the value explicitly.
pub trait Clock {
    fn now_epoch_seconds(&self) -> i64;
}

/// Input validation of a required identifier: surrounding whitespace is
/// removed and an empty value is rejected.
fn required_identifier(value: &str) -> Option<&str> {
    let trimmed = value.trim();
    (!trimmed.is_empty()).then_some(trimmed)
}

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

pub mod authorization;
pub mod error;
pub mod get_courier_current_delivery;
pub mod register_courier;
pub mod relationships;
pub mod set_courier_availability;
pub mod update_courier_location;

use crate::pkg::application::required_identifier;
use crate::slices::courier::domains::courier::CourierId;

use error::{CourierApplicationError, InvalidCourierRequest};

fn courier_id(value: &str) -> Result<CourierId, CourierApplicationError> {
    required_identifier(value)
        .map(CourierId::new)
        .ok_or(CourierApplicationError::InvalidRequest(
            InvalidCourierRequest::EmptyCourierId,
        ))
}

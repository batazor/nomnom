use crate::pkg::application::{AuthorizationError, Subject};
use crate::slices::courier::domains::courier::CourierId;

/// Permissions checked by the `courier` slice use cases.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum CourierPermission {
    RegisterCourier,
    SetCourierAvailability,
    UpdateCourierLocation,
    ViewCourierCurrentDelivery,
}

/// Resource of a courier permission check.
#[derive(Clone, Debug, Eq, PartialEq)]
pub enum CourierResource {
    /// Platform-wide operations such as registering a new courier.
    Platform,
    /// Operations on behalf of, or about, a concrete courier.
    Courier(CourierId),
}

/// Authorization port of the `courier` slice; the SpiceDB adapter lives in
/// `infrastructure/`.
pub trait CourierAuthorization {
    fn check(
        &self,
        subject: &Subject,
        permission: CourierPermission,
        resource: &CourierResource,
    ) -> Result<(), AuthorizationError>;
}

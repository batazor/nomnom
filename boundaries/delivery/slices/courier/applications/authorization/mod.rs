use crate::pkg::authz::{AuthorizationError, Subject};
use crate::slices::courier::domains::courier::CourierId;

/// Permissions checked by the `courier` slice use cases.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum CourierPermission {
    /// Courier Pool of the platform: register, retire and read couriers, and
    /// change the line of any courier (operator or dispatcher, FR-620).
    ManageCouriers,
    SetCourierAvailability,
    UpdateCourierLocation,
    ViewCourierCurrentDelivery,
}

/// Resource of a courier permission check.
#[derive(Clone, Debug, Eq, PartialEq)]
pub enum CourierResource {
    /// Platform-wide operations such as managing the Courier Pool.
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

/// Checks `permission` of the courier itself (`courier#act`); when it is
/// denied, the dispatcher right of the Courier Pool instead (FR-620). An
/// unavailable decision fails closed without the second check.
pub fn check_courier_or_dispatcher(
    authorization: &dyn CourierAuthorization,
    subject: &Subject,
    permission: CourierPermission,
    courier_id: &CourierId,
) -> Result<(), AuthorizationError> {
    match authorization.check(
        subject,
        permission,
        &CourierResource::Courier(courier_id.clone()),
    ) {
        Err(AuthorizationError::Denied) => authorization.check(
            subject,
            CourierPermission::ManageCouriers,
            &CourierResource::Platform,
        ),
        decision => decision,
    }
}

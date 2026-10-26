//! Transport error table of `delivery`: application errors of both slices →
//! gRPC status codes.

use tonic::Status;

use crate::slices::courier::applications::error::CourierApplicationError;
use crate::slices::delivery::applications::error::DeliveryApplicationError;
use crate::slices::simulator::applications::error::SimulatorApplicationError;

pub(super) fn delivery_transport_error(error: DeliveryApplicationError) -> Status {
    match error {
        DeliveryApplicationError::PermissionDenied => Status::permission_denied(error.to_string()),
        DeliveryApplicationError::InvalidRequest(_) => Status::invalid_argument(error.to_string()),
        DeliveryApplicationError::DeliveryNotFound(_)
        | DeliveryApplicationError::DeliveryForOrderNotFound(_)
        | DeliveryApplicationError::CourierNotFound(_)
        | DeliveryApplicationError::DeliveryOfferNotFound(_) => {
            Status::not_found(error.to_string())
        }
        // A repeat of Create Delivery with other values (ADR 41).
        DeliveryApplicationError::DeliveryForOrderInOtherMarket { .. } => {
            Status::already_exists(error.to_string())
        }
        DeliveryApplicationError::DeliveryPrecondition(_)
        | DeliveryApplicationError::CourierPrecondition(_)
        | DeliveryApplicationError::OfferPrecondition(_)
        | DeliveryApplicationError::CourierPositionUnknown(_) => {
            Status::failed_precondition(error.to_string())
        }
        // A lost optimistic concurrency race: the client retries the whole call.
        DeliveryApplicationError::ConcurrentModification => Status::aborted(error.to_string()),
        // An unknown commit outcome is resolved by an idempotent retry.
        DeliveryApplicationError::DependencyUnavailable(_)
        | DeliveryApplicationError::Transaction(_) => Status::unavailable(error.to_string()),
        DeliveryApplicationError::Internal(_) => Status::internal("internal error"),
    }
}

/// The same categories as [`delivery_transport_error`] for the `courier`
/// slice.
pub(super) fn courier_transport_error(error: CourierApplicationError) -> Status {
    match error {
        CourierApplicationError::PermissionDenied => Status::permission_denied(error.to_string()),
        CourierApplicationError::InvalidRequest(_) => Status::invalid_argument(error.to_string()),
        CourierApplicationError::CourierNotFound(_) => Status::not_found(error.to_string()),
        CourierApplicationError::CourierAlreadyRegistered(_) => {
            Status::already_exists(error.to_string())
        }
        CourierApplicationError::PreconditionFailed(_) => {
            Status::failed_precondition(error.to_string())
        }
        CourierApplicationError::ConcurrentModification => Status::aborted(error.to_string()),
        CourierApplicationError::DependencyUnavailable(_) => Status::unavailable(error.to_string()),
        CourierApplicationError::Internal(_) => Status::internal("internal error"),
    }
}

/// The same categories as [`delivery_transport_error`] for the `simulator`
/// slice.
pub(super) fn simulator_transport_error(error: SimulatorApplicationError) -> Status {
    match error {
        SimulatorApplicationError::PermissionDenied => Status::permission_denied(error.to_string()),
        SimulatorApplicationError::DependencyUnavailable(_) => {
            Status::unavailable(error.to_string())
        }
        SimulatorApplicationError::Internal(_) => Status::internal("internal error"),
    }
}

use std::error::Error;
use std::fmt::{Display, Formatter};

use crate::pkg::application::{AuthorizationError, RelationshipWriteError, UnitOfWorkError};
use crate::pkg::persistence::RepositoryError;
use crate::slices::courier::applications::assignment::CourierAssignmentError;
use crate::slices::courier::domains::courier::{CourierError, CourierId};
use crate::slices::delivery::domains::delivery::{
    DeliveryError, DeliveryId, DeliveryOfferError, DeliveryOfferId, DeliveryValidationError,
    OrderId, TransitionError,
};

use super::courier_locator::LocatorError;

#[derive(Clone, Debug, Eq, PartialEq)]
pub enum InvalidDeliveryRequest {
    EmptyIdentifier(&'static str),
    Validation(DeliveryValidationError),
}

impl Display for InvalidDeliveryRequest {
    fn fmt(&self, formatter: &mut Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::EmptyIdentifier(field) => write!(formatter, "{field} must not be empty"),
            Self::Validation(error) => Display::fmt(error, formatter),
        }
    }
}

/// Error of a `delivery` slice use case. Variants map one-to-one onto the
/// error categories of the `delivery` API.
#[derive(Clone, Debug, Eq, PartialEq)]
pub enum DeliveryApplicationError {
    PermissionDenied,
    InvalidRequest(InvalidDeliveryRequest),
    DeliveryNotFound(DeliveryId),
    /// No Delivery was created for the order.
    DeliveryForOrderNotFound(OrderId),
    CourierNotFound(CourierId),
    DeliveryOfferNotFound(DeliveryOfferId),
    /// The current `Delivery` state or assignment forbids the operation.
    DeliveryPrecondition(DeliveryError),
    /// The current `Courier` state forbids the operation.
    CourierPrecondition(CourierError),
    /// The current `Delivery Offer` state, addressee or acceptance policy
    /// forbids the operation.
    OfferPrecondition(DeliveryOfferError),
    /// No position snapshot exists for the courier.
    CourierPositionUnknown(CourierId),
    /// The aggregate was changed concurrently; the caller may retry.
    ConcurrentModification,
    DependencyUnavailable(String),
    /// The local transaction failed to commit or its outcome is unknown.
    Transaction(UnitOfWorkError),
    Internal(String),
}

impl Display for DeliveryApplicationError {
    fn fmt(&self, formatter: &mut Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::PermissionDenied => formatter.write_str("permission denied"),
            Self::InvalidRequest(error) => write!(formatter, "invalid request: {error}"),
            Self::DeliveryNotFound(id) => write!(formatter, "delivery {} not found", id.as_str()),
            Self::DeliveryForOrderNotFound(id) => {
                write!(formatter, "delivery of order {} not found", id.as_str())
            }
            Self::CourierNotFound(id) => write!(formatter, "courier {} not found", id.as_str()),
            Self::DeliveryOfferNotFound(id) => {
                write!(formatter, "delivery offer {} not found", id.as_str())
            }
            Self::DeliveryPrecondition(error) => {
                write!(formatter, "delivery precondition failed: {error}")
            }
            Self::CourierPrecondition(error) => {
                write!(formatter, "courier precondition failed: {error}")
            }
            Self::OfferPrecondition(error) => {
                write!(formatter, "delivery offer precondition failed: {error}")
            }
            Self::CourierPositionUnknown(id) => {
                write!(formatter, "position of courier {} is unknown", id.as_str())
            }
            Self::ConcurrentModification => formatter.write_str("concurrent modification, retry"),
            Self::DependencyUnavailable(reason) => {
                write!(formatter, "dependency unavailable: {reason}")
            }
            Self::Transaction(error) => Display::fmt(error, formatter),
            Self::Internal(reason) => write!(formatter, "internal error: {reason}"),
        }
    }
}

impl Error for DeliveryApplicationError {}

impl From<AuthorizationError> for DeliveryApplicationError {
    fn from(error: AuthorizationError) -> Self {
        match error {
            AuthorizationError::Denied => Self::PermissionDenied,
            AuthorizationError::Unavailable(reason) => Self::DependencyUnavailable(reason),
        }
    }
}

impl From<RepositoryError> for DeliveryApplicationError {
    fn from(error: RepositoryError) -> Self {
        match error {
            RepositoryError::Conflict => Self::Internal(error.to_string()),
            RepositoryError::ConcurrentModification => Self::ConcurrentModification,
            RepositoryError::Unavailable(reason) => Self::DependencyUnavailable(reason),
        }
    }
}

impl From<LocatorError> for DeliveryApplicationError {
    fn from(error: LocatorError) -> Self {
        match error {
            LocatorError::Unavailable(reason) => Self::DependencyUnavailable(reason),
        }
    }
}

impl From<UnitOfWorkError> for DeliveryApplicationError {
    fn from(error: UnitOfWorkError) -> Self {
        Self::Transaction(error)
    }
}

impl From<DeliveryError> for DeliveryApplicationError {
    fn from(error: DeliveryError) -> Self {
        Self::DeliveryPrecondition(error)
    }
}

impl From<TransitionError> for DeliveryApplicationError {
    fn from(error: TransitionError) -> Self {
        Self::DeliveryPrecondition(DeliveryError::Transition(error))
    }
}

impl From<CourierError> for DeliveryApplicationError {
    fn from(error: CourierError) -> Self {
        Self::CourierPrecondition(error)
    }
}

impl From<CourierAssignmentError> for DeliveryApplicationError {
    fn from(error: CourierAssignmentError) -> Self {
        match error {
            CourierAssignmentError::CourierNotFound(id) => Self::CourierNotFound(id),
            CourierAssignmentError::Precondition(error) => Self::CourierPrecondition(error),
            CourierAssignmentError::Repository(error) => error.into(),
        }
    }
}

impl From<DeliveryOfferError> for DeliveryApplicationError {
    fn from(error: DeliveryOfferError) -> Self {
        Self::OfferPrecondition(error)
    }
}

impl From<DeliveryValidationError> for DeliveryApplicationError {
    fn from(error: DeliveryValidationError) -> Self {
        Self::InvalidRequest(InvalidDeliveryRequest::Validation(error))
    }
}

impl From<RelationshipWriteError> for DeliveryApplicationError {
    fn from(error: RelationshipWriteError) -> Self {
        Self::DependencyUnavailable(error.to_string())
    }
}

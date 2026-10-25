use std::error::Error;
use std::fmt::{Display, Formatter};

use crate::pkg::application::AuthorizationError;
use crate::pkg::persistence::RepositoryError;
use crate::slices::courier::domains::courier::{CourierError, CourierId, LocationError};

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum InvalidCourierRequest {
    EmptyCourierId,
    Location(LocationError),
}

impl Display for InvalidCourierRequest {
    fn fmt(&self, formatter: &mut Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::EmptyCourierId => formatter.write_str("courier id must not be empty"),
            Self::Location(error) => Display::fmt(error, formatter),
        }
    }
}

/// Error of a `courier` slice use case.
#[derive(Clone, Debug, Eq, PartialEq)]
pub enum CourierApplicationError {
    PermissionDenied,
    InvalidRequest(InvalidCourierRequest),
    CourierNotFound(CourierId),
    CourierAlreadyRegistered(CourierId),
    /// The current courier state forbids the operation.
    PreconditionFailed(CourierError),
    DependencyUnavailable(String),
    Internal(String),
}

impl Display for CourierApplicationError {
    fn fmt(&self, formatter: &mut Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::PermissionDenied => formatter.write_str("permission denied"),
            Self::InvalidRequest(error) => write!(formatter, "invalid request: {error}"),
            Self::CourierNotFound(id) => write!(formatter, "courier {} not found", id.as_str()),
            Self::CourierAlreadyRegistered(id) => {
                write!(formatter, "courier {} is already registered", id.as_str())
            }
            Self::PreconditionFailed(error) => write!(formatter, "precondition failed: {error}"),
            Self::DependencyUnavailable(reason) => {
                write!(formatter, "dependency unavailable: {reason}")
            }
            Self::Internal(reason) => write!(formatter, "internal error: {reason}"),
        }
    }
}

impl Error for CourierApplicationError {}

impl From<AuthorizationError> for CourierApplicationError {
    fn from(error: AuthorizationError) -> Self {
        match error {
            AuthorizationError::Denied => Self::PermissionDenied,
            AuthorizationError::Unavailable(reason) => Self::DependencyUnavailable(reason),
        }
    }
}

impl From<RepositoryError> for CourierApplicationError {
    fn from(error: RepositoryError) -> Self {
        match error {
            RepositoryError::Conflict => Self::Internal(error.to_string()),
            RepositoryError::Unavailable(reason) => Self::DependencyUnavailable(reason),
        }
    }
}

impl From<CourierError> for CourierApplicationError {
    fn from(error: CourierError) -> Self {
        Self::PreconditionFailed(error)
    }
}

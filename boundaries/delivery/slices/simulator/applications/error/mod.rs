use std::error::Error;
use std::fmt::{Display, Formatter};

use crate::pkg::authz::AuthorizationError;
use crate::pkg::persistence::RepositoryError;

/// Error of a `simulator` slice use case.
#[derive(Clone, Debug, Eq, PartialEq)]
pub enum SimulatorApplicationError {
    PermissionDenied,
    /// Authorization or storage could not be reached.
    DependencyUnavailable(String),
    Internal(String),
}

impl Display for SimulatorApplicationError {
    fn fmt(&self, formatter: &mut Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::PermissionDenied => formatter.write_str("permission denied"),
            Self::DependencyUnavailable(reason) => {
                write!(formatter, "dependency unavailable: {reason}")
            }
            Self::Internal(reason) => write!(formatter, "internal error: {reason}"),
        }
    }
}

impl Error for SimulatorApplicationError {}

impl From<AuthorizationError> for SimulatorApplicationError {
    fn from(error: AuthorizationError) -> Self {
        match error {
            AuthorizationError::Denied => Self::PermissionDenied,
            AuthorizationError::Unavailable(reason) => Self::DependencyUnavailable(reason),
        }
    }
}

impl From<RepositoryError> for SimulatorApplicationError {
    fn from(error: RepositoryError) -> Self {
        match error {
            RepositoryError::Unavailable(reason) => Self::DependencyUnavailable(reason),
            RepositoryError::Conflict | RepositoryError::ConcurrentModification => {
                Self::Internal(error.to_string())
            }
        }
    }
}

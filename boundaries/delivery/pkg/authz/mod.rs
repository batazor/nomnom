//! Verified caller identity and authorization outcomes of the `delivery`
//! bounded context.
//!
//! Slice-specific permissions live next to the consuming use cases in
//! `slices/<slice>/applications/`.

use std::error::Error;
use std::fmt::{Display, Formatter};

/// Verified identity that invokes a use case.
#[derive(Clone, Debug, Eq, Hash, PartialEq)]
pub enum Subject {
    /// End user: courier, dispatcher or platform operator.
    User(String),
    /// Workload identity: Temporal Activity worker, consumer, background job.
    Service(String),
}

/// Outcome of an authoritative permission check that did not grant access.
#[derive(Clone, Debug, Eq, PartialEq)]
pub enum AuthorizationError {
    /// The subject is not allowed to perform the permission on the resource.
    Denied,
    /// The decision could not be made; protected operations fail closed.
    Unavailable(String),
}

impl Display for AuthorizationError {
    fn fmt(&self, formatter: &mut Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::Denied => formatter.write_str("permission denied"),
            Self::Unavailable(reason) => write!(formatter, "authorization unavailable: {reason}"),
        }
    }
}

impl Error for AuthorizationError {}

/// A relationship write that did not reach the authorization graph. Writers
/// run before the local commit, so the use case fails closed and persists
/// nothing (dual-write rules in `docs/security.md`).
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct RelationshipWriteError(pub String);

impl Display for RelationshipWriteError {
    fn fmt(&self, formatter: &mut Formatter<'_>) -> std::fmt::Result {
        write!(formatter, "relationship write failed: {}", self.0)
    }
}

impl Error for RelationshipWriteError {}

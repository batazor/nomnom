use std::error::Error;
use std::fmt::{Display, Formatter};

/// Technical failure of a storage-backed port: repositories, read models,
/// the Outbox and the courier location store.
#[derive(Clone, Debug, Eq, PartialEq)]
pub enum RepositoryError {
    /// A uniqueness or write precondition was violated by a concurrent or
    /// duplicate write.
    Conflict,
    /// The storage could not be reached or failed transiently.
    Unavailable(String),
}

impl Display for RepositoryError {
    fn fmt(&self, formatter: &mut Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::Conflict => formatter.write_str("storage write conflict"),
            Self::Unavailable(reason) => write!(formatter, "storage unavailable: {reason}"),
        }
    }
}

impl Error for RepositoryError {}

/// Authorization revision (SpiceDB ZedToken) of the last relationship write
/// made for a resource. Opaque: aggregates store it as a technical snapshot
/// field and never interpret it; the authorization adapter checks the
/// resource at least as fresh as this revision (FR-447).
#[derive(Clone, Debug, Eq, Hash, PartialEq)]
pub struct AuthorizationRevision(String);

impl AuthorizationRevision {
    #[must_use]
    pub fn new(token: impl Into<String>) -> Self {
        Self(token.into())
    }

    #[must_use]
    pub fn as_str(&self) -> &str {
        &self.0
    }
}

impl Display for AuthorizationRevision {
    fn fmt(&self, formatter: &mut Formatter<'_>) -> std::fmt::Result {
        formatter.write_str(&self.0)
    }
}

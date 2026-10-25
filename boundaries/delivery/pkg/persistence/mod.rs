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

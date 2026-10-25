use crate::pkg::persistence::RepositoryError;

use super::{Courier, CourierId};

/// Persistence contract of the `Courier` aggregate.
pub trait CourierRepository {
    fn get(&self, id: &CourierId) -> Result<Option<Courier>, RepositoryError>;

    /// Inserts a new courier; an existing id yields [`RepositoryError::Conflict`].
    fn add(&self, courier: &Courier) -> Result<(), RepositoryError>;

    /// Updates an existing courier; a missing id yields [`RepositoryError::Conflict`].
    fn save(&self, courier: &Courier) -> Result<(), RepositoryError>;
}

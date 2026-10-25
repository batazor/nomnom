use crate::pkg::persistence::RepositoryError;

use super::{Delivery, DeliveryId, DeliveryOffer, DeliveryOfferId, OrderId};

/// Persistence contract of the `Delivery` aggregate.
pub trait DeliveryRepository {
    fn get(&self, id: &DeliveryId) -> Result<Option<Delivery>, RepositoryError>;

    fn find_by_order(&self, order_id: &OrderId) -> Result<Option<Delivery>, RepositoryError>;

    /// Inserts a new delivery; an existing id or `OrderId` yields
    /// [`RepositoryError::Conflict`].
    fn add(&self, delivery: &Delivery) -> Result<(), RepositoryError>;

    /// Updates an existing delivery; a missing id yields
    /// [`RepositoryError::Conflict`].
    fn save(&self, delivery: &Delivery) -> Result<(), RepositoryError>;
}

/// Persistence contract of the `DeliveryOffer` aggregate.
pub trait DeliveryOfferRepository {
    fn get(&self, id: &DeliveryOfferId) -> Result<Option<DeliveryOffer>, RepositoryError>;

    /// Inserts a new offer; an existing id yields [`RepositoryError::Conflict`].
    fn add(&self, offer: &DeliveryOffer) -> Result<(), RepositoryError>;

    /// Updates an existing offer; a missing id yields
    /// [`RepositoryError::Conflict`].
    fn save(&self, offer: &DeliveryOffer) -> Result<(), RepositoryError>;
}

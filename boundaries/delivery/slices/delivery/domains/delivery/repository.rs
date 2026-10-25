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

    /// All offers of the delivery in any state, ordered by creation.
    fn list_for_delivery(
        &self,
        delivery_id: &DeliveryId,
    ) -> Result<Vec<DeliveryOffer>, RepositoryError>;

    /// Pending offers whose deadline has passed (`expires_at < now`), oldest
    /// deadline first, at most `limit`.
    fn list_expired_pending(
        &self,
        now_epoch_seconds: i64,
        limit: usize,
    ) -> Result<Vec<DeliveryOffer>, RepositoryError>;

    /// Inserts a new offer; an existing id yields [`RepositoryError::Conflict`].
    fn add(&self, offer: &DeliveryOffer) -> Result<(), RepositoryError>;

    /// Updates an existing offer; a missing id yields
    /// [`RepositoryError::Conflict`].
    fn save(&self, offer: &DeliveryOffer) -> Result<(), RepositoryError>;
}

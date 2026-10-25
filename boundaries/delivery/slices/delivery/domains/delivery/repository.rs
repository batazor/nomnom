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

    /// Waiting deliveries without a pending offer in creation order
    /// ([`DeliveryPosition`]), strictly after `after` when given, at most
    /// `limit`.
    fn list_waiting_without_pending_offer(
        &self,
        after: Option<&DeliveryPosition>,
        limit: usize,
    ) -> Result<Vec<Delivery>, RepositoryError>;
}

/// Place of a delivery in creation order: creation time, then id. A Delivery
/// stored before its creation time was recorded counts as created at `0`.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct DeliveryPosition {
    pub created_at_epoch_seconds: i64,
    pub delivery_id: DeliveryId,
}

impl DeliveryPosition {
    #[must_use]
    pub fn of(delivery: &Delivery) -> Self {
        Self {
            created_at_epoch_seconds: delivery.created_at_epoch_seconds().unwrap_or(0),
            delivery_id: delivery.id().clone(),
        }
    }

    /// Sort key of the order: ids compare by bytes, as the PostgreSQL
    /// `"C"` collation does.
    #[must_use]
    pub fn key(&self) -> (i64, &str) {
        (self.created_at_epoch_seconds, self.delivery_id.as_str())
    }
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

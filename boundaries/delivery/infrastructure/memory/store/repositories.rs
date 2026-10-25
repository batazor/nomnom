//! Repositories, the courier location store and the Outbox of [`MemoryStore`].

use crate::pkg::persistence::RepositoryError;
use crate::slices::courier::applications::update_courier_location::{
    CourierLocationStore, CourierPosition,
};
use crate::slices::courier::domains::courier::{Courier, CourierId, CourierRepository};
use crate::slices::delivery::applications::outbox::{DeliveryOutbox, OutboxMessage};
use crate::slices::delivery::domains::delivery::{
    Delivery, DeliveryId, DeliveryOffer, DeliveryOfferId, DeliveryOfferRepository,
    DeliveryRepository, OfferState, OrderId,
};

use super::{DeliveryRecord, MemoryStore};

impl CourierRepository for MemoryStore {
    fn get(&self, id: &CourierId) -> Result<Option<Courier>, RepositoryError> {
        Ok(self.available()?.couriers.get(id.as_str()).cloned())
    }

    fn add(&self, courier: &Courier) -> Result<(), RepositoryError> {
        let mut state = self.available()?;
        if state.couriers.contains_key(courier.id().as_str()) {
            return Err(RepositoryError::Conflict);
        }
        state
            .couriers
            .insert(courier.id().as_str().to_owned(), courier.clone());
        Ok(())
    }

    fn save(&self, courier: &Courier) -> Result<(), RepositoryError> {
        let mut state = self.available()?;
        let stored = state
            .couriers
            .get_mut(courier.id().as_str())
            .ok_or(RepositoryError::Conflict)?;
        if stored.version() != courier.version() {
            return Err(RepositoryError::ConcurrentModification);
        }
        *stored = courier.clone().with_version(courier.version() + 1);
        Ok(())
    }
}

impl CourierLocationStore for MemoryStore {
    fn record(&self, position: &CourierPosition) -> Result<(), RepositoryError> {
        self.available()?
            .positions
            .insert(position.courier_id.as_str().to_owned(), position.clone());
        Ok(())
    }

    fn latest(&self, courier_id: &CourierId) -> Result<Option<CourierPosition>, RepositoryError> {
        Ok(self
            .available()?
            .positions
            .get(courier_id.as_str())
            .cloned())
    }
}

impl DeliveryRepository for MemoryStore {
    fn get(&self, id: &DeliveryId) -> Result<Option<Delivery>, RepositoryError> {
        self.available()?
            .deliveries
            .get(id.as_str())
            .map(DeliveryRecord::to_domain)
            .transpose()
    }

    fn find_by_order(&self, order_id: &OrderId) -> Result<Option<Delivery>, RepositoryError> {
        self.available()?
            .deliveries
            .values()
            .find(|record| &record.order_id == order_id)
            .map(DeliveryRecord::to_domain)
            .transpose()
    }

    fn add(&self, delivery: &Delivery) -> Result<(), RepositoryError> {
        let mut state = self.available()?;
        let duplicate = state.deliveries.contains_key(delivery.id().as_str())
            || state
                .deliveries
                .values()
                .any(|record| &record.order_id == delivery.order_id());
        if duplicate {
            return Err(RepositoryError::Conflict);
        }
        state.deliveries.insert(
            delivery.id().as_str().to_owned(),
            DeliveryRecord::from_domain(delivery),
        );
        Ok(())
    }

    fn save(&self, delivery: &Delivery) -> Result<(), RepositoryError> {
        let mut state = self.available()?;
        let stored = state
            .deliveries
            .get_mut(delivery.id().as_str())
            .ok_or(RepositoryError::Conflict)?;
        if stored.version != delivery.version() {
            return Err(RepositoryError::ConcurrentModification);
        }
        *stored = DeliveryRecord::from_domain(delivery);
        stored.version += 1;
        Ok(())
    }
}

impl DeliveryOfferRepository for MemoryStore {
    fn get(&self, id: &DeliveryOfferId) -> Result<Option<DeliveryOffer>, RepositoryError> {
        Ok(self
            .available()?
            .offers
            .iter()
            .find(|offer| offer.id() == id)
            .cloned())
    }

    fn list_for_delivery(
        &self,
        delivery_id: &DeliveryId,
    ) -> Result<Vec<DeliveryOffer>, RepositoryError> {
        Ok(self
            .available()?
            .offers
            .iter()
            .filter(|offer| offer.delivery_id() == delivery_id)
            .cloned()
            .collect())
    }

    fn list_expired_pending(
        &self,
        now_epoch_seconds: i64,
        limit: usize,
    ) -> Result<Vec<DeliveryOffer>, RepositoryError> {
        let mut expired: Vec<DeliveryOffer> = self
            .available()?
            .offers
            .iter()
            .filter(|offer| {
                offer.state() == OfferState::Pending
                    && offer.expires_at_epoch_seconds() < now_epoch_seconds
            })
            .cloned()
            .collect();
        // Stable sort keeps insertion order among equal deadlines.
        expired.sort_by_key(DeliveryOffer::expires_at_epoch_seconds);
        expired.truncate(limit);
        Ok(expired)
    }

    fn add(&self, offer: &DeliveryOffer) -> Result<(), RepositoryError> {
        let mut state = self.available()?;
        if state.offers.iter().any(|stored| stored.id() == offer.id()) {
            return Err(RepositoryError::Conflict);
        }
        state.offers.push(offer.clone());
        Ok(())
    }

    fn save(&self, offer: &DeliveryOffer) -> Result<(), RepositoryError> {
        let mut state = self.available()?;
        let stored = state
            .offers
            .iter_mut()
            .find(|stored| stored.id() == offer.id())
            .ok_or(RepositoryError::Conflict)?;
        if stored.version() != offer.version() {
            return Err(RepositoryError::ConcurrentModification);
        }
        *stored = offer.clone().with_version(offer.version() + 1);
        Ok(())
    }
}

impl DeliveryOutbox for MemoryStore {
    fn add(&self, message: OutboxMessage) -> Result<(), RepositoryError> {
        self.available()?.outbox.push(message);
        Ok(())
    }
}

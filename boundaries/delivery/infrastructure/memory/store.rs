use std::collections::BTreeMap;
use std::sync::atomic::{AtomicBool, AtomicUsize, Ordering};
use std::sync::{Mutex, MutexGuard, PoisonError};

use crate::pkg::application::{UnitOfWork, UnitOfWorkError};
use crate::pkg::persistence::{AuthorizationRevision, RepositoryError};
use crate::slices::courier::applications::get_courier_current_delivery::{
    CourierCurrentDeliveryReadModel, CurrentDeliveryView,
};
use crate::slices::courier::applications::update_courier_location::{
    CourierLocationStore, CourierPosition,
};
use crate::slices::courier::domains::courier::{Courier, CourierId, CourierRepository};
use crate::slices::delivery::applications::list_available_delivery_offers::{
    AvailableDeliveryOfferView, PendingDeliveryOffersReadModel,
};
use crate::slices::delivery::applications::outbox::{DeliveryOutbox, OutboxMessage};
use crate::slices::delivery::domains::delivery::{
    Address, Delivery, DeliveryFailureReason, DeliveryId, DeliveryOffer, DeliveryOfferId,
    DeliveryOfferRepository, DeliveryRepository, OfferState, OrderId, State,
};

/// Persistence model of `Delivery`: domain events are never stored.
#[derive(Clone, Debug)]
struct DeliveryRecord {
    id: DeliveryId,
    order_id: OrderId,
    courier_id: Option<CourierId>,
    pickup_address: Address,
    dropoff_address: Address,
    state: State,
    failure_reason: Option<DeliveryFailureReason>,
    authorization_revision: Option<AuthorizationRevision>,
}

impl DeliveryRecord {
    fn from_domain(delivery: &Delivery) -> Self {
        Self {
            id: delivery.id().clone(),
            order_id: delivery.order_id().clone(),
            courier_id: delivery.courier_id().cloned(),
            pickup_address: delivery.pickup_address().clone(),
            dropoff_address: delivery.dropoff_address().clone(),
            state: delivery.state(),
            failure_reason: delivery.failure_reason().cloned(),
            authorization_revision: delivery.authorization_revision().cloned(),
        }
    }

    fn to_domain(&self) -> Result<Delivery, RepositoryError> {
        Delivery::restore(
            self.id.clone(),
            self.order_id.clone(),
            self.courier_id.clone(),
            self.pickup_address.clone(),
            self.dropoff_address.clone(),
            self.state,
            self.failure_reason.clone(),
        )
        .map(|delivery| delivery.with_authorization_revision(self.authorization_revision.clone()))
        .map_err(|error| RepositoryError::Unavailable(format!("corrupt delivery record: {error}")))
    }
}

#[derive(Clone, Debug, Default)]
struct StoreState {
    couriers: BTreeMap<String, Courier>,
    deliveries: BTreeMap<String, DeliveryRecord>,
    offers: Vec<DeliveryOffer>,
    outbox: Vec<OutboxMessage>,
    positions: BTreeMap<String, CourierPosition>,
}

/// In-memory storage of the `delivery` bounded context for tests.
///
/// One store implements every repository, the Outbox, the courier location
/// store, the query read models and the Unit of Work, so a rollback discards
/// all writes made inside the transaction. Transactions are serialized by a
/// global lock; nested scopes are not supported.
#[derive(Debug, Default)]
pub struct MemoryStore {
    state: Mutex<StoreState>,
    transaction: Mutex<()>,
    unavailable: AtomicBool,
    fail_next_commit: Mutex<Option<UnitOfWorkError>>,
    commits: AtomicUsize,
    rollbacks: AtomicUsize,
}

impl MemoryStore {
    #[must_use]
    pub fn new() -> Self {
        Self::default()
    }

    /// Simulates a storage outage for every port of the store.
    pub fn set_unavailable(&self, unavailable: bool) {
        self.unavailable.store(unavailable, Ordering::SeqCst);
    }

    /// Makes the next Unit of Work fail at commit after its work succeeded.
    pub fn fail_next_commit(&self, error: UnitOfWorkError) {
        *self
            .fail_next_commit
            .lock()
            .unwrap_or_else(PoisonError::into_inner) = Some(error);
    }

    /// Messages committed to the Outbox, in insertion order.
    #[must_use]
    pub fn outbox_messages(&self) -> Vec<OutboxMessage> {
        self.state().outbox.clone()
    }

    #[must_use]
    pub fn commits(&self) -> usize {
        self.commits.load(Ordering::SeqCst)
    }

    #[must_use]
    pub fn rollbacks(&self) -> usize {
        self.rollbacks.load(Ordering::SeqCst)
    }

    fn state(&self) -> MutexGuard<'_, StoreState> {
        self.state.lock().unwrap_or_else(PoisonError::into_inner)
    }

    fn available(&self) -> Result<MutexGuard<'_, StoreState>, RepositoryError> {
        if self.unavailable.load(Ordering::SeqCst) {
            return Err(RepositoryError::Unavailable(
                "memory store offline".to_owned(),
            ));
        }
        Ok(self.state())
    }
}

/// Business side of relationship reconciliation. Reads wait for the running
/// Unit of Work and see only committed state, so a relationship deleted
/// before commit is not restored from uncommitted data. Must not be called
/// inside a Unit of Work (the transaction lock is not reentrant).
impl MemoryStore {
    fn committed<T>(&self, read: impl FnOnce(&StoreState) -> T) -> Result<T, RepositoryError> {
        let _transaction = self
            .transaction
            .lock()
            .unwrap_or_else(PoisonError::into_inner);
        let state = self.available()?;
        Ok(read(&state))
    }

    /// Registered courier ids after `after`, ordered by id.
    pub fn courier_page(&self, after: &str, limit: usize) -> Result<Vec<String>, RepositoryError> {
        self.committed(|state| {
            state
                .couriers
                .keys()
                .filter(|id| id.as_str() > after)
                .take(limit)
                .cloned()
                .collect()
        })
    }

    /// Which of `ids` are registered couriers.
    pub fn registered_couriers(&self, ids: &[String]) -> Result<Vec<String>, RepositoryError> {
        self.committed(|state| {
            ids.iter()
                .filter(|id| state.couriers.contains_key(id.as_str()))
                .cloned()
                .collect()
        })
    }

    /// `(delivery_id, courier_id)` of deliveries with an assigned courier
    /// after `after`, ordered by delivery id.
    pub fn assignment_page(
        &self,
        after: &str,
        limit: usize,
    ) -> Result<Vec<(String, String)>, RepositoryError> {
        self.committed(|state| {
            state
                .deliveries
                .range::<str, _>((std::ops::Bound::Excluded(after), std::ops::Bound::Unbounded))
                .filter_map(|(id, record)| {
                    record
                        .courier_id
                        .as_ref()
                        .map(|courier| (id.clone(), courier.as_str().to_owned()))
                })
                .take(limit)
                .collect()
        })
    }

    /// Stores the revision of a relationship restored by reconciliation on a
    /// registered courier. `false` — the courier is not registered.
    pub fn record_courier_revision(
        &self,
        courier_id: &str,
        revision: AuthorizationRevision,
    ) -> Result<bool, RepositoryError> {
        let _transaction = self
            .transaction
            .lock()
            .unwrap_or_else(PoisonError::into_inner);
        let mut state = self.available()?;
        let Some(courier) = state.couriers.get_mut(courier_id) else {
            return Ok(false);
        };
        courier.record_authorization_revision(revision);
        Ok(true)
    }

    /// Stores the revision of a restored `delivery#courier` relationship when
    /// the delivery is still assigned to `courier_id`.
    pub fn record_assignment_revision(
        &self,
        delivery_id: &str,
        courier_id: &str,
        revision: AuthorizationRevision,
    ) -> Result<bool, RepositoryError> {
        let _transaction = self
            .transaction
            .lock()
            .unwrap_or_else(PoisonError::into_inner);
        let mut state = self.available()?;
        let Some(record) = state.deliveries.get_mut(delivery_id) else {
            return Ok(false);
        };
        if record.courier_id.as_ref().map(CourierId::as_str) != Some(courier_id) {
            return Ok(false);
        }
        record.authorization_revision = Some(revision);
        Ok(true)
    }

    /// `(delivery_id, courier_id)` for those of `ids` with an assigned courier.
    pub fn assigned_couriers(
        &self,
        ids: &[String],
    ) -> Result<Vec<(String, String)>, RepositoryError> {
        self.committed(|state| {
            ids.iter()
                .filter_map(|id| {
                    let courier = state.deliveries.get(id.as_str())?.courier_id.as_ref()?;
                    Some((id.clone(), courier.as_str().to_owned()))
                })
                .collect()
        })
    }
}

impl UnitOfWork for MemoryStore {
    fn run<T, E, F>(&self, work: F) -> Result<T, E>
    where
        F: FnOnce() -> Result<T, E>,
        E: From<UnitOfWorkError>,
    {
        let _transaction = self
            .transaction
            .lock()
            .unwrap_or_else(PoisonError::into_inner);
        let snapshot = self.state().clone();
        let result = work().and_then(|value| {
            let failure = self
                .fail_next_commit
                .lock()
                .unwrap_or_else(PoisonError::into_inner)
                .take();
            failure.map_or(Ok(value), |error| Err(E::from(error)))
        });
        if result.is_ok() {
            self.commits.fetch_add(1, Ordering::SeqCst);
        } else {
            *self.state() = snapshot;
            self.rollbacks.fetch_add(1, Ordering::SeqCst);
        }
        result
    }
}

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
        *stored = courier.clone();
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
        *stored = DeliveryRecord::from_domain(delivery);
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
        *stored = offer.clone();
        Ok(())
    }
}

impl DeliveryOutbox for MemoryStore {
    fn add(&self, message: OutboxMessage) -> Result<(), RepositoryError> {
        self.available()?.outbox.push(message);
        Ok(())
    }
}

impl CourierCurrentDeliveryReadModel for MemoryStore {
    fn active_delivery_for(
        &self,
        courier_id: &CourierId,
    ) -> Result<Option<CurrentDeliveryView>, RepositoryError> {
        Ok(self
            .available()?
            .deliveries
            .values()
            .find(|record| {
                record.courier_id.as_ref() == Some(courier_id)
                    && matches!(record.state, State::Assigned | State::PickedUp)
            })
            .map(|record| CurrentDeliveryView {
                delivery_id: record.id.clone(),
                order_id: record.order_id.clone(),
                pickup_address: record.pickup_address.clone(),
                dropoff_address: record.dropoff_address.clone(),
                state: record.state,
            }))
    }
}

impl PendingDeliveryOffersReadModel for MemoryStore {
    fn pending_offers_for(
        &self,
        courier_id: &CourierId,
    ) -> Result<Vec<AvailableDeliveryOfferView>, RepositoryError> {
        let state = self.available()?;
        Ok(state
            .offers
            .iter()
            .filter(|offer| {
                offer.courier_id() == courier_id && offer.state() == OfferState::Pending
            })
            .filter_map(|offer| {
                state
                    .deliveries
                    .get(offer.delivery_id().as_str())
                    .map(|delivery| AvailableDeliveryOfferView {
                        offer_id: offer.id().clone(),
                        delivery_id: offer.delivery_id().clone(),
                        pickup_address: delivery.pickup_address.clone(),
                        dropoff_address: delivery.dropoff_address.clone(),
                        expires_at_epoch_seconds: offer.expires_at_epoch_seconds(),
                    })
            })
            .collect())
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::slices::courier::domains::courier::CourierStatus;

    #[derive(Debug, PartialEq)]
    enum TestError {
        Work,
        Transaction(UnitOfWorkError),
    }

    impl From<UnitOfWorkError> for TestError {
        fn from(error: UnitOfWorkError) -> Self {
            Self::Transaction(error)
        }
    }

    fn courier(id: &str) -> Courier {
        Courier::new(CourierId::new(id))
    }

    #[test]
    fn unit_of_work_commits_successful_work() {
        let store = MemoryStore::new();
        let result: Result<(), TestError> = store.run(|| {
            CourierRepository::add(&store, &courier("courier-1")).map_err(|_| TestError::Work)
        });

        assert_eq!(result, Ok(()));
        assert!(
            CourierRepository::get(&store, &CourierId::new("courier-1"))
                .expect("read")
                .is_some()
        );
        assert_eq!((store.commits(), store.rollbacks()), (1, 0));
    }

    #[test]
    fn unit_of_work_rolls_back_failed_work() {
        let store = MemoryStore::new();
        CourierRepository::add(&store, &courier("courier-1")).expect("seed");

        let result: Result<(), TestError> = store.run(|| {
            let busy = Courier::restore(CourierId::new("courier-1"), CourierStatus::Busy);
            CourierRepository::save(&store, &busy).map_err(|_| TestError::Work)?;
            CourierRepository::add(&store, &courier("courier-2")).map_err(|_| TestError::Work)?;
            Err(TestError::Work)
        });

        assert_eq!(result, Err(TestError::Work));
        assert_eq!(
            CourierRepository::get(&store, &CourierId::new("courier-1")),
            Ok(Some(courier("courier-1")))
        );
        assert_eq!(
            CourierRepository::get(&store, &CourierId::new("courier-2")),
            Ok(None)
        );
        assert_eq!((store.commits(), store.rollbacks()), (0, 1));
    }

    #[test]
    fn unit_of_work_rolls_back_on_commit_failure() {
        let store = MemoryStore::new();
        store.fail_next_commit(UnitOfWorkError::CommitFailed("disk full".to_owned()));

        let result: Result<(), TestError> = store.run(|| {
            CourierRepository::add(&store, &courier("courier-1")).map_err(|_| TestError::Work)
        });

        assert_eq!(
            result,
            Err(TestError::Transaction(UnitOfWorkError::CommitFailed(
                "disk full".to_owned()
            )))
        );
        assert_eq!(
            CourierRepository::get(&store, &CourierId::new("courier-1")),
            Ok(None)
        );
        let next: Result<(), TestError> = store.run(|| Ok(()));
        assert_eq!(next, Ok(()));
    }

    #[test]
    fn delivery_uniqueness_covers_order_id() {
        let store = MemoryStore::new();
        let delivery = |id: &str| {
            Delivery::new(
                DeliveryId::new(id),
                OrderId::new("order-1"),
                Address::new("a").expect("address"),
                Address::new("b").expect("address"),
            )
        };
        DeliveryRepository::add(&store, &delivery("delivery-1")).expect("first");
        assert_eq!(
            DeliveryRepository::add(&store, &delivery("delivery-2")),
            Err(RepositoryError::Conflict)
        );
    }

    #[test]
    fn unavailable_store_rejects_every_port() {
        let store = MemoryStore::new();
        store.set_unavailable(true);
        assert!(matches!(
            CourierRepository::get(&store, &CourierId::new("courier-1")),
            Err(RepositoryError::Unavailable(_))
        ));
        assert!(matches!(
            DeliveryRepository::get(&store, &DeliveryId::new("delivery-1")),
            Err(RepositoryError::Unavailable(_))
        ));
    }
}

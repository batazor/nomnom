mod read_models;
mod reconciliation;
mod repositories;

use std::collections::BTreeMap;
use std::sync::atomic::{AtomicBool, AtomicUsize, Ordering};
use std::sync::{Mutex, MutexGuard, PoisonError};

use crate::pkg::application::{UnitOfWork, UnitOfWorkError};
use crate::pkg::persistence::{AuthorizationRevision, RepositoryError};
use crate::slices::courier::applications::update_courier_location::CourierPosition;
use crate::slices::courier::domains::courier::{Courier, CourierId};
use crate::slices::delivery::applications::outbox::OutboxMessage;
use crate::slices::delivery::domains::delivery::{
    Address, Delivery, DeliveryFailureReason, DeliveryId, DeliveryOffer, OrderId, State,
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
    version: u64,
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
            version: delivery.version(),
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
        .map(|delivery| {
            delivery
                .with_authorization_revision(self.authorization_revision.clone())
                .with_version(self.version)
        })
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

#[cfg(test)]
mod tests;

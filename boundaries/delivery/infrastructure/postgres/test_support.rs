//! PostgreSQL of the unit tests: one testcontainers PostgreSQL per test
//! binary, a migrated template database and a fresh database per
//! [`TestStore`], so tests keep their fixed ids and run in parallel.
//!
//! [`TestStore`] is [`PostgresStore`] with test hooks: a storage outage
//! switch, a commit failure, Unit of Work counters and the Outbox messages
//! the use cases committed (the PostgreSQL Outbox keeps only mapped
//! Integration Events). Every read and write goes to PostgreSQL.

use std::sync::atomic::{AtomicBool, AtomicUsize, Ordering};
use std::sync::{Mutex, MutexGuard, OnceLock, PoisonError};

use sqlx::Connection as _;
use testcontainers_modules::postgres::Postgres;
use testcontainers_modules::testcontainers::runners::SyncRunner;
use testcontainers_modules::testcontainers::{Container, ImageExt};

use crate::infrastructure::locator::CourierPositions;
use crate::infrastructure::reconcile::ReconciliationStore;
use crate::pkg::application::{UnitOfWork, UnitOfWorkError};
use crate::pkg::config::DatabaseConfig;
use crate::pkg::persistence::{AuthorizationRevision, RepositoryError};
use crate::slices::courier::applications::update_courier_location::{
    CourierLocationStore, CourierPosition,
};
use crate::slices::courier::domains::courier::{Courier, CourierId, CourierRepository};
use crate::slices::delivery::applications::dispatch_deliveries::{
    DeliveryPosition, WaitingDeliveries,
};
use crate::slices::delivery::applications::expire_delivery_offers::ExpiredDeliveryOffers;
use crate::slices::delivery::applications::get_courier_current_delivery::{
    CourierCurrentDeliveryReadModel, CurrentDeliveryView,
};
use crate::slices::delivery::applications::list_available_delivery_offers::{
    AvailableDeliveryOfferView, PendingDeliveryOffersReadModel,
};
use crate::slices::delivery::applications::offers_of_delivery::OffersOfDelivery;
use crate::slices::delivery::applications::outbox::{DeliveryOutbox, OutboxMessage};
use crate::slices::delivery::domains::delivery::{
    Delivery, DeliveryId, DeliveryOffer, DeliveryOfferId, DeliveryOfferRepository,
    DeliveryRepository, OrderId,
};

use super::{PostgresStore, blocking, migrate};

/// Image of the platform PostgreSQL (ops).
const IMAGE_TAG: &str = "18.6-alpine3.24";
const TEMPLATE: &str = "delivery_template";

static CONTAINER: Mutex<Option<Container<Postgres>>> = Mutex::new(None);
static SERVER: OnceLock<String> = OnceLock::new();
static NEXT_DATABASE: AtomicUsize = AtomicUsize::new(1);

/// Statics are never dropped: the container is removed when the test binary
/// exits.
extern "C" fn remove_container() {
    drop(
        CONTAINER
            .lock()
            .unwrap_or_else(PoisonError::into_inner)
            .take(),
    );
}

/// `postgres://user:password@host:port` of the container, started once with
/// the migrated template database.
fn server() -> &'static str {
    SERVER.get_or_init(|| {
        blocking(|| {
            let container = Postgres::default()
                .with_tag(IMAGE_TAG)
                .with_cmd(["postgres", "-c", "max_connections=1000"])
                .start()
                .expect("start postgres container");
            let host = container.get_host().expect("container host");
            let port = container.get_host_port_ipv4(5432).expect("container port");
            *CONTAINER.lock().unwrap_or_else(PoisonError::into_inner) = Some(container);
            // SAFETY: registers a plain `extern "C"` function without arguments.
            unsafe { libc::atexit(remove_container) };
            let server = format!("postgres://postgres:postgres@{host}:{port}");
            admin(&server, &format!("CREATE DATABASE {TEMPLATE}"));
            migrate(&config(format!("{server}/{TEMPLATE}"))).expect("migrate template");
            server
        })
    })
}

fn admin(server: &str, statement: &str) {
    let url = format!("{server}/postgres");
    let statement = statement.to_owned();
    blocking(move || {
        tokio::runtime::Builder::new_current_thread()
            .enable_all()
            .build()
            .expect("admin runtime")
            .block_on(async move {
                let mut connection = sqlx::PgConnection::connect(&url)
                    .await
                    .expect("admin connection");
                sqlx::raw_sql(sqlx::AssertSqlSafe(statement))
                    .execute(&mut connection)
                    .await
                    .expect("admin statement");
                connection.close().await.expect("close admin connection");
            });
    });
}

fn config(url: String) -> DatabaseConfig {
    DatabaseConfig {
        url,
        max_conns: 4,
        ..DatabaseConfig::default()
    }
}

/// A new migrated database of the shared container.
pub(crate) fn fresh_database() -> DatabaseConfig {
    let server = server();
    let name = format!(
        "delivery_test_{}_{}",
        std::process::id(),
        NEXT_DATABASE.fetch_add(1, Ordering::Relaxed)
    );
    admin(
        server,
        &format!("CREATE DATABASE {name} TEMPLATE {TEMPLATE}"),
    );
    config(format!("{server}/{name}"))
}

/// [`PostgresStore`] over a fresh database with test hooks.
pub struct TestStore {
    inner: PostgresStore,
    unavailable: AtomicBool,
    fail_next_commit: Mutex<Option<UnitOfWorkError>>,
    outbox: Mutex<Vec<OutboxMessage>>,
    commits: AtomicUsize,
    rollbacks: AtomicUsize,
}

impl Default for TestStore {
    fn default() -> Self {
        Self::new()
    }
}

impl TestStore {
    #[must_use]
    pub fn new() -> Self {
        Self {
            inner: PostgresStore::connect(&fresh_database()).expect("connect test database"),
            unavailable: AtomicBool::new(false),
            fail_next_commit: Mutex::new(None),
            outbox: Mutex::new(Vec::new()),
            commits: AtomicUsize::new(0),
            rollbacks: AtomicUsize::new(0),
        }
    }

    /// Every port fails as if PostgreSQL were unreachable.
    pub fn set_unavailable(&self, unavailable: bool) {
        self.unavailable.store(unavailable, Ordering::SeqCst);
    }

    /// The next Unit of Work rolls back after its work succeeded and reports
    /// `error`.
    pub fn fail_next_commit(&self, error: UnitOfWorkError) {
        *self
            .fail_next_commit
            .lock()
            .unwrap_or_else(PoisonError::into_inner) = Some(error);
    }

    /// Outbox messages that were committed (or written outside a Unit of
    /// Work), in insertion order.
    #[must_use]
    pub fn outbox_messages(&self) -> Vec<OutboxMessage> {
        self.messages().clone()
    }

    #[must_use]
    pub fn commits(&self) -> usize {
        self.commits.load(Ordering::SeqCst)
    }

    #[must_use]
    pub fn rollbacks(&self) -> usize {
        self.rollbacks.load(Ordering::SeqCst)
    }

    fn messages(&self) -> MutexGuard<'_, Vec<OutboxMessage>> {
        self.outbox.lock().unwrap_or_else(PoisonError::into_inner)
    }

    fn available(&self) -> Result<&PostgresStore, RepositoryError> {
        if self.unavailable.load(Ordering::SeqCst) {
            return Err(RepositoryError::Unavailable(
                "test store offline".to_owned(),
            ));
        }
        Ok(&self.inner)
    }
}

impl UnitOfWork for TestStore {
    fn run<T, E, F>(&self, work: F) -> Result<T, E>
    where
        F: FnOnce() -> Result<T, E>,
        E: From<UnitOfWorkError>,
    {
        let mark = self.messages().len();
        let result = self.inner.run(|| {
            let value = work()?;
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
            self.messages().truncate(mark);
            self.rollbacks.fetch_add(1, Ordering::SeqCst);
        }
        result
    }
}

impl CourierRepository for TestStore {
    fn get(&self, id: &CourierId) -> Result<Option<Courier>, RepositoryError> {
        CourierRepository::get(self.available()?, id)
    }

    fn add(&self, courier: &Courier) -> Result<(), RepositoryError> {
        CourierRepository::add(self.available()?, courier)
    }

    fn save(&self, courier: &Courier) -> Result<(), RepositoryError> {
        CourierRepository::save(self.available()?, courier)
    }
}

impl CourierLocationStore for TestStore {
    fn record(&self, position: &CourierPosition) -> Result<(), RepositoryError> {
        self.available()?.record(position)
    }

    fn latest(&self, courier_id: &CourierId) -> Result<Option<CourierPosition>, RepositoryError> {
        self.available()?.latest(courier_id)
    }
}

impl DeliveryRepository for TestStore {
    fn get(&self, id: &DeliveryId) -> Result<Option<Delivery>, RepositoryError> {
        DeliveryRepository::get(self.available()?, id)
    }

    fn find_by_order(&self, order_id: &OrderId) -> Result<Option<Delivery>, RepositoryError> {
        self.available()?.find_by_order(order_id)
    }

    fn add(&self, delivery: &Delivery) -> Result<(), RepositoryError> {
        DeliveryRepository::add(self.available()?, delivery)
    }

    fn save(&self, delivery: &Delivery) -> Result<(), RepositoryError> {
        DeliveryRepository::save(self.available()?, delivery)
    }
}

impl DeliveryOfferRepository for TestStore {
    fn get(&self, id: &DeliveryOfferId) -> Result<Option<DeliveryOffer>, RepositoryError> {
        DeliveryOfferRepository::get(self.available()?, id)
    }

    fn add(&self, offer: &DeliveryOffer) -> Result<(), RepositoryError> {
        DeliveryOfferRepository::add(self.available()?, offer)
    }

    fn save(&self, offer: &DeliveryOffer) -> Result<(), RepositoryError> {
        DeliveryOfferRepository::save(self.available()?, offer)
    }
}

impl OffersOfDelivery for TestStore {
    fn offer_ids_of(
        &self,
        delivery_id: &DeliveryId,
    ) -> Result<Vec<DeliveryOfferId>, RepositoryError> {
        self.available()?.offer_ids_of(delivery_id)
    }
}

impl ExpiredDeliveryOffers for TestStore {
    fn expired_pending_offer_ids(
        &self,
        now_epoch_seconds: i64,
        limit: usize,
    ) -> Result<Vec<DeliveryOfferId>, RepositoryError> {
        self.available()?
            .expired_pending_offer_ids(now_epoch_seconds, limit)
    }
}

impl DeliveryOutbox for TestStore {
    fn add(&self, message: OutboxMessage) -> Result<(), RepositoryError> {
        DeliveryOutbox::add(self.available()?, message.clone())?;
        self.messages().push(message);
        Ok(())
    }
}

impl CourierCurrentDeliveryReadModel for TestStore {
    fn active_delivery_for(
        &self,
        courier_id: &CourierId,
    ) -> Result<Option<CurrentDeliveryView>, RepositoryError> {
        self.available()?.active_delivery_for(courier_id)
    }
}

impl PendingDeliveryOffersReadModel for TestStore {
    fn pending_offers_for(
        &self,
        courier_id: &CourierId,
    ) -> Result<Vec<AvailableDeliveryOfferView>, RepositoryError> {
        self.available()?.pending_offers_for(courier_id)
    }
}

impl WaitingDeliveries for TestStore {
    fn list_waiting_without_pending_offer(
        &self,
        after: Option<&DeliveryPosition>,
        limit: usize,
    ) -> Result<Vec<Delivery>, RepositoryError> {
        self.available()?
            .list_waiting_without_pending_offer(after, limit)
    }
}

impl CourierPositions for TestStore {
    fn available_positions(&self) -> Result<Vec<CourierPosition>, RepositoryError> {
        self.available()?.available_positions()
    }
}

impl ReconciliationStore for TestStore {
    fn courier_page(
        &self,
        after: &str,
        limit: usize,
    ) -> Result<Vec<(String, u64)>, RepositoryError> {
        self.available()?.courier_page(after, limit)
    }

    fn registered_couriers(&self, ids: &[String]) -> Result<Vec<String>, RepositoryError> {
        self.available()?.registered_couriers(ids)
    }

    fn assignment_page(
        &self,
        after: &str,
        limit: usize,
    ) -> Result<Vec<(String, String, u64)>, RepositoryError> {
        self.available()?.assignment_page(after, limit)
    }

    fn assigned_couriers(&self, ids: &[String]) -> Result<Vec<(String, String)>, RepositoryError> {
        self.available()?.assigned_couriers(ids)
    }

    fn record_courier_revision(
        &self,
        courier_id: &str,
        revision: AuthorizationRevision,
    ) -> Result<bool, RepositoryError> {
        self.available()?
            .record_courier_revision(courier_id, revision)
    }

    fn record_assignment_revision(
        &self,
        delivery_id: &str,
        courier_id: &str,
        revision: AuthorizationRevision,
    ) -> Result<bool, RepositoryError> {
        self.available()?
            .record_assignment_revision(delivery_id, courier_id, revision)
    }
}

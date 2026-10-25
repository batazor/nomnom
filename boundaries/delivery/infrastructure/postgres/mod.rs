//! PostgreSQL storage of the `delivery` bounded context (ADR 8, ADR 31,
//! ADR 0001 of the context) on `sqlx`: repositories with optimistic
//! concurrency, the Transactional Outbox (ADR 13, ADR 31), the courier
//! location store, read models, the Unit of Work and the reconciliation reads.
//!
//! Optimistic concurrency: every aggregate row carries `version`. `save`
//! runs `UPDATE ... SET version = version + 1 WHERE id = $1 AND version = $2`
//! with the version the aggregate was loaded at; zero rows on an existing id
//! is [`RepositoryError::ConcurrentModification`].
//!
//! Sync ports over async sqlx: the store owns a small tokio runtime that
//! drives the pool. Each call runs `block_on` on it through [`blocking`]:
//! `block_in_place` on a multi-thread runtime (as the SpiceDB client does), a
//! scoped thread on a current-thread runtime (tests), directly otherwise.
//!
//! Unit of Work: the ports take no transaction handle, so the store keeps the
//! connection of the running transaction in a thread-local slot keyed by the
//! store. Use cases run synchronously on one thread, so every repository and
//! Outbox call inside `run` uses that connection and commits or rolls back
//! together.

mod migrate;

use std::cell::RefCell;
use std::future::Future;
use std::sync::Arc;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::time::Duration;

use sqlx::pool::PoolConnection;
use sqlx::postgres::{PgPool, PgPoolOptions, PgRow};
use sqlx::{Postgres, Row};
use tokio::runtime::{Handle, Runtime, RuntimeFlavor};
use uuid::Uuid;

pub use migrate::{MIGRATOR, migrate};

use crate::infrastructure::reconcile::ReconciliationStore;
use crate::pkg::application::{UnitOfWork, UnitOfWorkError};
use crate::pkg::config::{DATABASE_URL_ENV, DatabaseConfig};
use crate::pkg::persistence::{AuthorizationRevision, RepositoryError};
use crate::slices::courier::applications::update_courier_location::{
    CourierLocationStore, CourierPosition,
};
use crate::slices::courier::domains::courier::{
    Courier, CourierId, CourierRepository, CourierStatus, Location,
};
use crate::slices::delivery::applications::get_courier_current_delivery::{
    CourierCurrentDeliveryReadModel, CurrentDeliveryView,
};
use crate::slices::delivery::applications::list_available_delivery_offers::{
    AvailableDeliveryOfferView, PendingDeliveryOffersReadModel,
};
use crate::slices::delivery::applications::outbox::{DeliveryOutbox, OutboxMessage};
use crate::slices::delivery::domains::delivery::{
    Address, Delivery, DeliveryFailureReason, DeliveryId, DeliveryOffer, DeliveryOfferId,
    DeliveryOfferRepository, DeliveryRepository, OfferState, OrderId, State,
};

/// How long a caller waits for a free connection.
const ACQUIRE_TIMEOUT: Duration = Duration::from_secs(5);

/// Kafka topic of `delivery` Integration Events (ADR 31).
pub const OUTBOX_TOPIC: &str = "delivery.v1.delivery-events";

/// Namespace of `event_id` UUIDv5 (ADR 31).
const EVENT_ID_NAMESPACE: Uuid = Uuid::from_u128(0x8f1d_3c2a_5b7e_4a90_9c61_2e4f_7b8d_0a13);

/// `event_id` = UUIDv5 of `"{aggregate_type}:{aggregate_id}:{aggregate_version}:{event_type}"`.
#[must_use]
pub fn event_id(
    aggregate_type: &str,
    aggregate_id: &str,
    aggregate_version: i64,
    event_type: &str,
) -> Uuid {
    Uuid::new_v5(
        &EVENT_ID_NAMESPACE,
        format!("{aggregate_type}:{aggregate_id}:{aggregate_version}:{event_type}").as_bytes(),
    )
}

/// Integration Event of an Outbox row (ADR 31).
#[derive(Clone, Debug, PartialEq)]
pub struct OutboxRecord {
    /// Full Protobuf message name, e.g. `nomnom.delivery.v1.DeliveryFailed`.
    pub event_type: String,
    /// Protobuf payload of the contract.
    pub payload: Vec<u8>,
    /// `traceparent`, `causation_id`.
    pub metadata: serde_json::Value,
}

/// Mapper Domain Event → Integration Event. `None` — the event has no
/// contract and is not written to the Outbox.
pub trait OutboxMapper: Send + Sync {
    fn map(&self, message: &OutboxMessage) -> Option<OutboxRecord>;
}

/// Default mapper until the Protobuf mappers land: nothing is published.
pub struct NoIntegrationEvents;

impl OutboxMapper for NoIntegrationEvents {
    fn map(&self, _message: &OutboxMessage) -> Option<OutboxRecord> {
        None
    }
}

/// Runs blocking work without blocking an async worker in place.
pub(crate) fn blocking<T: Send>(work: impl FnOnce() -> T + Send) -> T {
    match Handle::try_current() {
        Ok(handle) if handle.runtime_flavor() == RuntimeFlavor::MultiThread => {
            tokio::task::block_in_place(work)
        }
        Ok(_) => std::thread::scope(|scope| {
            scope
                .spawn(work)
                .join()
                .unwrap_or_else(|panic| std::panic::resume_unwind(panic))
        }),
        Err(_) => work(),
    }
}

type Connection = PoolConnection<Postgres>;

static NEXT_STORE_ID: AtomicUsize = AtomicUsize::new(1);

thread_local! {
    /// Connections of transactions running on this thread, by store id.
    static ACTIVE: RefCell<Vec<(usize, Connection)>> = const { RefCell::new(Vec::new()) };
}

fn take_active(store: usize) -> Option<Connection> {
    ACTIVE.with(|active| {
        let mut active = active.borrow_mut();
        let index = active.iter().position(|(id, _)| *id == store)?;
        Some(active.swap_remove(index).1)
    })
}

fn put_active(store: usize, connection: Connection) {
    ACTIVE.with(|active| active.borrow_mut().push((store, connection)));
}

/// A connection borrowed for one statement: the transaction's or a pooled one.
struct Lease {
    connection: Connection,
    in_transaction: bool,
}

/// PostgreSQL adapter of every storage port of `delivery`.
pub struct PostgresStore {
    id: usize,
    pool: Option<PgPool>,
    mapper: Arc<dyn OutboxMapper>,
    runtime: Option<Runtime>,
}

impl PostgresStore {
    /// Opens the pool and checks connectivity. An empty URL fails startup.
    pub fn connect(config: &DatabaseConfig) -> Result<Self, String> {
        if !config.enabled() {
            return Err(format!("{DATABASE_URL_ENV} is required"));
        }
        let runtime = tokio::runtime::Builder::new_multi_thread()
            .worker_threads(2)
            .thread_name("delivery-postgres")
            .enable_all()
            .build()
            .map_err(|error| format!("postgres runtime: {error}"))?;
        let url = config.url.clone();
        let max = config.max_conns;
        let pool = blocking(|| {
            runtime.block_on(async move {
                PgPoolOptions::new()
                    .max_connections(max)
                    .acquire_timeout(ACQUIRE_TIMEOUT)
                    .connect(&url)
                    .await
            })
        });
        let pool = match pool {
            Ok(pool) => pool,
            Err(error) => {
                runtime.shutdown_background();
                return Err(format!("connect postgres: {error}"));
            }
        };
        Ok(Self {
            id: NEXT_STORE_ID.fetch_add(1, Ordering::Relaxed),
            pool: Some(pool),
            mapper: Arc::new(NoIntegrationEvents),
            runtime: Some(runtime),
        })
    }

    /// Replaces the Domain Event → Integration Event mapper of the Outbox.
    #[must_use]
    pub fn with_outbox_mapper(mut self, mapper: Arc<dyn OutboxMapper>) -> Self {
        self.mapper = mapper;
        self
    }

    fn runtime(&self) -> &Runtime {
        self.runtime.as_ref().expect("runtime lives until drop")
    }

    fn pool(&self) -> PgPool {
        self.pool.clone().expect("pool lives until drop")
    }

    fn block_on<F>(&self, future: F) -> F::Output
    where
        F: Future + Send,
        F::Output: Send,
    {
        let runtime = self.runtime();
        blocking(|| runtime.block_on(future))
    }

    fn lease(&self) -> Result<Lease, RepositoryError> {
        if let Some(connection) = take_active(self.id) {
            return Ok(Lease {
                connection,
                in_transaction: true,
            });
        }
        let pool = self.pool();
        let connection = self
            .block_on(async move { pool.acquire().await })
            .map_err(storage_error)?;
        Ok(Lease {
            connection,
            in_transaction: false,
        })
    }

    fn release(&self, lease: Lease) {
        if lease.in_transaction {
            put_active(self.id, lease.connection);
        } else {
            self.drop_connection(lease.connection);
        }
    }

    /// Returning a connection to the pool spawns onto the sqlx runtime.
    fn drop_connection(&self, connection: Connection) {
        let _context = self.runtime().enter();
        drop(connection);
    }

    fn discard_connection(&self, mut connection: Connection) {
        connection.close_on_drop();
        self.drop_connection(connection);
    }
}

impl Drop for PostgresStore {
    fn drop(&mut self) {
        if let Some(runtime) = self.runtime.take() {
            {
                let _context = runtime.enter();
                drop(self.pool.take());
            }
            // Dropping a runtime blocks; shutdown_background works in an async
            // context too.
            runtime.shutdown_background();
        }
    }
}

/// Runs one statement on the leased connection and maps the sqlx error.
/// Dynamic statements are `format!` over column-list constants only
/// (`AssertSqlSafe`); values are always bound.
macro_rules! sql {
    ($store:expr, |$connection:ident| $body:expr) => {{
        let mut lease = $store.lease()?;
        let result = {
            let $connection: &mut sqlx::PgConnection = &mut *lease.connection;
            $store.block_on(async move { $body.await })
        };
        $store.release(lease);
        result.map_err(storage_error)
    }};
}

fn storage_error(error: sqlx::Error) -> RepositoryError {
    if let Some(database) = error.as_database_error() {
        match database.code().as_deref() {
            Some("23505") => return RepositoryError::Conflict,
            Some("40001" | "40P01") => return RepositoryError::ConcurrentModification,
            _ => {}
        }
    }
    RepositoryError::Unavailable(format!("postgres: {error}"))
}

fn corrupt(what: &str, detail: impl std::fmt::Display) -> RepositoryError {
    RepositoryError::Unavailable(format!("corrupt {what} row: {detail}"))
}

fn db_version(version: u64) -> Result<i64, RepositoryError> {
    i64::try_from(version).map_err(|_| corrupt("version", version))
}

fn domain_version(version: i64) -> Result<u64, RepositoryError> {
    u64::try_from(version).map_err(|_| corrupt("version", version))
}

fn limit(limit: usize) -> i64 {
    i64::try_from(limit).unwrap_or(i64::MAX)
}

/// Guard of a running transaction: a panic inside the work drops the
/// connection (closed, rolled back by the server) instead of leaking it.
struct ActiveTransaction<'a> {
    store: &'a PostgresStore,
    finished: bool,
}

impl Drop for ActiveTransaction<'_> {
    fn drop(&mut self) {
        if !self.finished
            && let Some(connection) = take_active(self.store.id)
        {
            self.store.discard_connection(connection);
        }
    }
}

async fn execute(
    connection: &mut sqlx::PgConnection,
    statement: &'static str,
) -> Result<(), sqlx::Error> {
    sqlx::raw_sql(statement).execute(connection).await.map(drop)
}

impl UnitOfWork for PostgresStore {
    fn run<T, E, F>(&self, work: F) -> Result<T, E>
    where
        F: FnOnce() -> Result<T, E>,
        E: From<UnitOfWorkError>,
    {
        let begin_failed =
            |reason: String| E::from(UnitOfWorkError::CommitFailed(format!("begin: {reason}")));
        if let Some(connection) = take_active(self.id) {
            put_active(self.id, connection);
            return Err(begin_failed("nested unit of work".to_owned()));
        }
        let mut lease = self
            .lease()
            .map_err(|error| begin_failed(error.to_string()))?;
        if let Err(error) = self.block_on(execute(&mut lease.connection, "BEGIN")) {
            self.drop_connection(lease.connection);
            return Err(begin_failed(error.to_string()));
        }
        put_active(self.id, lease.connection);
        let mut guard = ActiveTransaction {
            store: self,
            finished: false,
        };
        let result = work();
        guard.finished = true;
        let Some(mut connection) = take_active(self.id) else {
            return Err(E::from(UnitOfWorkError::CommitOutcomeUnknown));
        };
        match result {
            Ok(value) => match self.block_on(execute(&mut connection, "COMMIT")) {
                Ok(()) => {
                    self.drop_connection(connection);
                    Ok(value)
                }
                Err(error) => {
                    // An I/O failure leaves the outcome unknown; a server
                    // rejection (for example a deferred constraint) rolled back.
                    let outcome = if error.as_database_error().is_some() {
                        UnitOfWorkError::CommitFailed(error.to_string())
                    } else {
                        UnitOfWorkError::CommitOutcomeUnknown
                    };
                    self.discard_connection(connection);
                    Err(E::from(outcome))
                }
            },
            Err(error) => {
                match self.block_on(execute(&mut connection, "ROLLBACK")) {
                    Ok(()) => self.drop_connection(connection),
                    Err(_) => self.discard_connection(connection),
                }
                Err(error)
            }
        }
    }
}

fn courier_status(status: CourierStatus) -> &'static str {
    match status {
        CourierStatus::Available => "available",
        CourierStatus::Busy => "busy",
        CourierStatus::Unavailable => "unavailable",
    }
}

fn parse_courier_status(value: &str) -> Result<CourierStatus, RepositoryError> {
    match value {
        "available" => Ok(CourierStatus::Available),
        "busy" => Ok(CourierStatus::Busy),
        "unavailable" => Ok(CourierStatus::Unavailable),
        other => Err(corrupt("courier status", other)),
    }
}

fn delivery_state(state: State) -> &'static str {
    match state {
        State::Waiting => "waiting",
        State::Assigned => "assigned",
        State::PickedUp => "picked_up",
        State::Delivered => "delivered",
        State::Failed => "failed",
        State::Cancelled => "cancelled",
    }
}

fn parse_delivery_state(value: &str) -> Result<State, RepositoryError> {
    match value {
        "waiting" => Ok(State::Waiting),
        "assigned" => Ok(State::Assigned),
        "picked_up" => Ok(State::PickedUp),
        "delivered" => Ok(State::Delivered),
        "failed" => Ok(State::Failed),
        "cancelled" => Ok(State::Cancelled),
        other => Err(corrupt("delivery state", other)),
    }
}

fn offer_state(state: OfferState) -> &'static str {
    match state {
        OfferState::Pending => "pending",
        OfferState::Accepted => "accepted",
        OfferState::Rejected => "rejected",
        OfferState::Withdrawn => "withdrawn",
    }
}

fn parse_offer_state(value: &str) -> Result<OfferState, RepositoryError> {
    match value {
        "pending" => Ok(OfferState::Pending),
        "accepted" => Ok(OfferState::Accepted),
        "rejected" => Ok(OfferState::Rejected),
        "withdrawn" => Ok(OfferState::Withdrawn),
        other => Err(corrupt("offer state", other)),
    }
}

fn address(value: &str) -> Result<Address, RepositoryError> {
    Address::new(value).map_err(|error| corrupt("address", error))
}

fn column<'r, T>(row: &'r PgRow, name: &str) -> Result<T, RepositoryError>
where
    T: sqlx::Decode<'r, Postgres> + sqlx::Type<Postgres>,
{
    row.try_get(name).map_err(|error| corrupt(name, error))
}

const COURIER_COLUMNS: &str = "id, status, authorization_revision, version";

fn courier_from_row(row: &PgRow) -> Result<Courier, RepositoryError> {
    let status = parse_courier_status(column(row, "status")?)?;
    let revision: Option<String> = column(row, "authorization_revision")?;
    Ok(
        Courier::restore(CourierId::new(column::<String>(row, "id")?), status)
            .with_authorization_revision(revision.map(AuthorizationRevision::new))
            .with_version(domain_version(column(row, "version")?)?),
    )
}

const DELIVERY_COLUMNS: &str = "id, order_id, courier_id, pickup_address, dropoff_address, \
     state, failure_reason, authorization_revision, version";

fn delivery_from_row(row: &PgRow) -> Result<Delivery, RepositoryError> {
    let failure_reason = column::<Option<String>>(row, "failure_reason")?
        .map(|reason| {
            DeliveryFailureReason::new(&reason).map_err(|error| corrupt("failure reason", error))
        })
        .transpose()?;
    let revision: Option<String> = column(row, "authorization_revision")?;
    let version = domain_version(column(row, "version")?)?;
    Delivery::restore(
        DeliveryId::new(column::<String>(row, "id")?),
        OrderId::new(column::<String>(row, "order_id")?),
        column::<Option<String>>(row, "courier_id")?.map(CourierId::new),
        address(column(row, "pickup_address")?)?,
        address(column(row, "dropoff_address")?)?,
        parse_delivery_state(column(row, "state")?)?,
        failure_reason,
    )
    .map_err(|error| corrupt("delivery", error))
    .map(|delivery| {
        delivery
            .with_authorization_revision(revision.map(AuthorizationRevision::new))
            .with_version(version)
    })
}

const OFFER_COLUMNS: &str = "id, delivery_id, courier_id, offered_at, expires_at, state, version";

fn offer_from_row(row: &PgRow) -> Result<DeliveryOffer, RepositoryError> {
    let version = domain_version(column(row, "version")?)?;
    DeliveryOffer::restore(
        DeliveryOfferId::new(column::<String>(row, "id")?),
        DeliveryId::new(column::<String>(row, "delivery_id")?),
        CourierId::new(column::<String>(row, "courier_id")?),
        column(row, "offered_at")?,
        column(row, "expires_at")?,
        parse_offer_state(column(row, "state")?)?,
    )
    .map_err(|error| corrupt("delivery offer", error))
    .map(|offer| offer.with_version(version))
}

impl PostgresStore {
    /// Maps zero updated rows: a missing id is a [`RepositoryError::Conflict`]
    /// (port contract), an existing id a lost optimistic concurrency race.
    fn optimistic(
        &self,
        updated: u64,
        exists_sql: &'static str,
        id: &str,
    ) -> Result<(), RepositoryError> {
        if updated > 0 {
            return Ok(());
        }
        let exists = sql!(self, |connection| sqlx::query(exists_sql)
            .bind(id)
            .fetch_optional(connection))?
        .is_some();
        Err(if exists {
            RepositoryError::ConcurrentModification
        } else {
            RepositoryError::Conflict
        })
    }
}

impl CourierRepository for PostgresStore {
    fn get(&self, id: &CourierId) -> Result<Option<Courier>, RepositoryError> {
        let statement = format!("SELECT {COURIER_COLUMNS} FROM couriers WHERE id = $1");
        sql!(self, |connection| sqlx::query(sqlx::AssertSqlSafe(
            statement
        ))
        .bind(id.as_str())
        .fetch_optional(connection))?
        .as_ref()
        .map(courier_from_row)
        .transpose()
    }

    fn add(&self, courier: &Courier) -> Result<(), RepositoryError> {
        let version = db_version(courier.version())?;
        let inserted = sql!(self, |connection| sqlx::query(
            "INSERT INTO couriers (id, status, authorization_revision, version)
             VALUES ($1, $2, $3, $4) ON CONFLICT DO NOTHING"
        )
        .bind(courier.id().as_str())
        .bind(courier_status(courier.status()))
        .bind(
            courier
                .authorization_revision()
                .map(AuthorizationRevision::as_str)
        )
        .bind(version)
        .execute(connection))?;
        if inserted.rows_affected() == 0 {
            return Err(RepositoryError::Conflict);
        }
        Ok(())
    }

    fn save(&self, courier: &Courier) -> Result<(), RepositoryError> {
        let version = db_version(courier.version())?;
        let updated = sql!(self, |connection| sqlx::query(
            "UPDATE couriers SET status = $3, authorization_revision = $4,
                 version = version + 1
             WHERE id = $1 AND version = $2"
        )
        .bind(courier.id().as_str())
        .bind(version)
        .bind(courier_status(courier.status()))
        .bind(
            courier
                .authorization_revision()
                .map(AuthorizationRevision::as_str)
        )
        .execute(connection))?;
        self.optimistic(
            updated.rows_affected(),
            "SELECT 1 FROM couriers WHERE id = $1",
            courier.id().as_str(),
        )
    }
}

impl DeliveryRepository for PostgresStore {
    fn get(&self, id: &DeliveryId) -> Result<Option<Delivery>, RepositoryError> {
        let statement = format!("SELECT {DELIVERY_COLUMNS} FROM deliveries WHERE id = $1");
        sql!(self, |connection| sqlx::query(sqlx::AssertSqlSafe(
            statement
        ))
        .bind(id.as_str())
        .fetch_optional(connection))?
        .as_ref()
        .map(delivery_from_row)
        .transpose()
    }

    fn find_by_order(&self, order_id: &OrderId) -> Result<Option<Delivery>, RepositoryError> {
        let statement = format!("SELECT {DELIVERY_COLUMNS} FROM deliveries WHERE order_id = $1");
        sql!(self, |connection| sqlx::query(sqlx::AssertSqlSafe(
            statement
        ))
        .bind(order_id.as_str())
        .fetch_optional(connection))?
        .as_ref()
        .map(delivery_from_row)
        .transpose()
    }

    fn add(&self, delivery: &Delivery) -> Result<(), RepositoryError> {
        let version = db_version(delivery.version())?;
        // ON CONFLICT DO NOTHING keeps the transaction usable after a
        // duplicate id or order id.
        let inserted = sql!(self, |connection| sqlx::query(
            "INSERT INTO deliveries (id, order_id, courier_id, pickup_address,
                 dropoff_address, state, failure_reason, authorization_revision, version)
             VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) ON CONFLICT DO NOTHING"
        )
        .bind(delivery.id().as_str())
        .bind(delivery.order_id().as_str())
        .bind(delivery.courier_id().map(CourierId::as_str))
        .bind(delivery.pickup_address().as_str())
        .bind(delivery.dropoff_address().as_str())
        .bind(delivery_state(delivery.state()))
        .bind(delivery.failure_reason().map(DeliveryFailureReason::as_str))
        .bind(
            delivery
                .authorization_revision()
                .map(AuthorizationRevision::as_str)
        )
        .bind(version)
        .execute(connection))?;
        if inserted.rows_affected() == 0 {
            return Err(RepositoryError::Conflict);
        }
        Ok(())
    }

    fn save(&self, delivery: &Delivery) -> Result<(), RepositoryError> {
        let version = db_version(delivery.version())?;
        let updated = sql!(self, |connection| sqlx::query(
            "UPDATE deliveries SET courier_id = $3, state = $4, failure_reason = $5,
                 authorization_revision = $6, version = version + 1
             WHERE id = $1 AND version = $2"
        )
        .bind(delivery.id().as_str())
        .bind(version)
        .bind(delivery.courier_id().map(CourierId::as_str))
        .bind(delivery_state(delivery.state()))
        .bind(delivery.failure_reason().map(DeliveryFailureReason::as_str))
        .bind(
            delivery
                .authorization_revision()
                .map(AuthorizationRevision::as_str)
        )
        .execute(connection))?;
        self.optimistic(
            updated.rows_affected(),
            "SELECT 1 FROM deliveries WHERE id = $1",
            delivery.id().as_str(),
        )
    }
}

impl DeliveryOfferRepository for PostgresStore {
    fn get(&self, id: &DeliveryOfferId) -> Result<Option<DeliveryOffer>, RepositoryError> {
        let statement = format!("SELECT {OFFER_COLUMNS} FROM delivery_offers WHERE id = $1");
        sql!(self, |connection| sqlx::query(sqlx::AssertSqlSafe(
            statement
        ))
        .bind(id.as_str())
        .fetch_optional(connection))?
        .as_ref()
        .map(offer_from_row)
        .transpose()
    }

    fn list_for_delivery(
        &self,
        delivery_id: &DeliveryId,
    ) -> Result<Vec<DeliveryOffer>, RepositoryError> {
        let statement = format!(
            "SELECT {OFFER_COLUMNS} FROM delivery_offers WHERE delivery_id = $1 ORDER BY position"
        );
        sql!(self, |connection| sqlx::query(sqlx::AssertSqlSafe(
            statement
        ))
        .bind(delivery_id.as_str())
        .fetch_all(connection))?
        .iter()
        .map(offer_from_row)
        .collect()
    }

    fn add(&self, offer: &DeliveryOffer) -> Result<(), RepositoryError> {
        let version = db_version(offer.version())?;
        let inserted = sql!(self, |connection| sqlx::query(
            "INSERT INTO delivery_offers (id, delivery_id, courier_id, offered_at,
                 expires_at, state, version)
             VALUES ($1, $2, $3, $4, $5, $6, $7) ON CONFLICT DO NOTHING"
        )
        .bind(offer.id().as_str())
        .bind(offer.delivery_id().as_str())
        .bind(offer.courier_id().as_str())
        .bind(offer.offered_at_epoch_seconds())
        .bind(offer.expires_at_epoch_seconds())
        .bind(offer_state(offer.state()))
        .bind(version)
        .execute(connection))?;
        if inserted.rows_affected() == 0 {
            return Err(RepositoryError::Conflict);
        }
        Ok(())
    }

    fn save(&self, offer: &DeliveryOffer) -> Result<(), RepositoryError> {
        let version = db_version(offer.version())?;
        let updated = sql!(self, |connection| sqlx::query(
            "UPDATE delivery_offers SET state = $3, version = version + 1
             WHERE id = $1 AND version = $2"
        )
        .bind(offer.id().as_str())
        .bind(version)
        .bind(offer_state(offer.state()))
        .execute(connection))?;
        self.optimistic(
            updated.rows_affected(),
            "SELECT 1 FROM delivery_offers WHERE id = $1",
            offer.id().as_str(),
        )
    }
}

impl DeliveryOutbox for PostgresStore {
    /// Inside a Unit of Work the row commits or rolls back with the aggregate;
    /// `aggregate_version` is the version the same transaction just wrote.
    /// Events the mapper does not publish are not written.
    fn add(&self, message: OutboxMessage) -> Result<(), RepositoryError> {
        let Some(record) = self.mapper.map(&message) else {
            return Ok(());
        };
        let aggregate_id = message.aggregate_id.as_str();
        let version: Option<i64> = sql!(self, |connection| sqlx::query_scalar(
            "SELECT version FROM deliveries WHERE id = $1"
        )
        .bind(aggregate_id)
        .fetch_optional(connection))?;
        let version = version.ok_or_else(|| {
            RepositoryError::Unavailable(format!(
                "outbox event {} for unknown delivery {aggregate_id}",
                record.event_type
            ))
        })?;
        let id = event_id(
            message.aggregate_type,
            aggregate_id,
            version,
            &record.event_type,
        )
        .to_string();
        let metadata = record.metadata.to_string();
        sql!(self, |connection| sqlx::query(
            "INSERT INTO delivery_outbox (event_id, event_type, aggregate_type, aggregate_id,
                 aggregate_version, topic, payload, metadata)
             VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8::jsonb)"
        )
        .bind(&id)
        .bind(&record.event_type)
        .bind(message.aggregate_type)
        .bind(aggregate_id)
        .bind(version)
        .bind(OUTBOX_TOPIC)
        .bind(&record.payload)
        .bind(&metadata)
        .execute(connection))
        .map(drop)
    }
}

impl CourierLocationStore for PostgresStore {
    fn record(&self, position: &CourierPosition) -> Result<(), RepositoryError> {
        sql!(self, |connection| sqlx::query(
            "INSERT INTO courier_positions (courier_id, latitude, longitude, recorded_at)
             VALUES ($1, $2, $3, $4)
             ON CONFLICT (courier_id) DO UPDATE SET latitude = EXCLUDED.latitude,
                 longitude = EXCLUDED.longitude, recorded_at = EXCLUDED.recorded_at"
        )
        .bind(position.courier_id.as_str())
        .bind(position.location.latitude())
        .bind(position.location.longitude())
        .bind(position.recorded_at_epoch_seconds)
        .execute(connection))
        .map(drop)
    }

    fn latest(&self, courier_id: &CourierId) -> Result<Option<CourierPosition>, RepositoryError> {
        sql!(self, |connection| sqlx::query(
            "SELECT latitude, longitude, recorded_at FROM courier_positions
             WHERE courier_id = $1"
        )
        .bind(courier_id.as_str())
        .fetch_optional(connection))?
        .map(|row| {
            Ok(CourierPosition {
                courier_id: courier_id.clone(),
                location: Location::new(column(&row, "latitude")?, column(&row, "longitude")?)
                    .map_err(|error| corrupt("courier position", error))?,
                recorded_at_epoch_seconds: column(&row, "recorded_at")?,
            })
        })
        .transpose()
    }
}

impl CourierCurrentDeliveryReadModel for PostgresStore {
    fn active_delivery_for(
        &self,
        courier_id: &CourierId,
    ) -> Result<Option<CurrentDeliveryView>, RepositoryError> {
        sql!(self, |connection| sqlx::query(
            "SELECT id, order_id, pickup_address, dropoff_address, state FROM deliveries
             WHERE courier_id = $1 AND state IN ('assigned', 'picked_up')
             ORDER BY id LIMIT 1"
        )
        .bind(courier_id.as_str())
        .fetch_optional(connection))?
        .map(|row| {
            Ok(CurrentDeliveryView {
                delivery_id: DeliveryId::new(column::<String>(&row, "id")?),
                order_id: OrderId::new(column::<String>(&row, "order_id")?),
                pickup_address: address(column(&row, "pickup_address")?)?,
                dropoff_address: address(column(&row, "dropoff_address")?)?,
                state: parse_delivery_state(column(&row, "state")?)?,
            })
        })
        .transpose()
    }
}

impl PendingDeliveryOffersReadModel for PostgresStore {
    fn pending_offers_for(
        &self,
        courier_id: &CourierId,
    ) -> Result<Vec<AvailableDeliveryOfferView>, RepositoryError> {
        sql!(self, |connection| sqlx::query(
            "SELECT o.id, o.delivery_id, d.pickup_address, d.dropoff_address, o.expires_at
             FROM delivery_offers o JOIN deliveries d ON d.id = o.delivery_id
             WHERE o.courier_id = $1 AND o.state = 'pending'
             ORDER BY o.position"
        )
        .bind(courier_id.as_str())
        .fetch_all(connection))?
        .iter()
        .map(|row| {
            Ok(AvailableDeliveryOfferView {
                offer_id: DeliveryOfferId::new(column::<String>(row, "id")?),
                delivery_id: DeliveryId::new(column::<String>(row, "delivery_id")?),
                pickup_address: address(column(row, "pickup_address")?)?,
                dropoff_address: address(column(row, "dropoff_address")?)?,
                expires_at_epoch_seconds: column(row, "expires_at")?,
            })
        })
        .collect()
    }
}

impl ReconciliationStore for PostgresStore {
    fn courier_page(&self, after: &str, size: usize) -> Result<Vec<String>, RepositoryError> {
        sql!(self, |connection| sqlx::query_scalar(
            "SELECT id FROM couriers WHERE id > $1 ORDER BY id LIMIT $2"
        )
        .bind(after)
        .bind(limit(size))
        .fetch_all(connection))
    }

    fn registered_couriers(&self, ids: &[String]) -> Result<Vec<String>, RepositoryError> {
        sql!(self, |connection| sqlx::query_scalar(
            "SELECT id FROM couriers WHERE id = ANY($1)"
        )
        .bind(ids)
        .fetch_all(connection))
    }

    fn assignment_page(
        &self,
        after: &str,
        size: usize,
    ) -> Result<Vec<(String, String)>, RepositoryError> {
        sql!(self, |connection| sqlx::query_as(
            "SELECT id, courier_id FROM deliveries
             WHERE id > $1 AND courier_id IS NOT NULL ORDER BY id LIMIT $2"
        )
        .bind(after)
        .bind(limit(size))
        .fetch_all(connection))
    }

    fn assigned_couriers(&self, ids: &[String]) -> Result<Vec<(String, String)>, RepositoryError> {
        sql!(self, |connection| sqlx::query_as(
            "SELECT id, courier_id FROM deliveries
             WHERE id = ANY($1) AND courier_id IS NOT NULL"
        )
        .bind(ids)
        .fetch_all(connection))
    }

    fn record_courier_revision(
        &self,
        courier_id: &str,
        revision: AuthorizationRevision,
    ) -> Result<bool, RepositoryError> {
        let updated = sql!(self, |connection| sqlx::query(
            "UPDATE couriers SET authorization_revision = $2, version = version + 1
             WHERE id = $1"
        )
        .bind(courier_id)
        .bind(revision.as_str())
        .execute(connection))?;
        Ok(updated.rows_affected() > 0)
    }

    fn record_assignment_revision(
        &self,
        delivery_id: &str,
        courier_id: &str,
        revision: AuthorizationRevision,
    ) -> Result<bool, RepositoryError> {
        let updated = sql!(self, |connection| sqlx::query(
            "UPDATE deliveries SET authorization_revision = $3, version = version + 1
             WHERE id = $1 AND courier_id = $2"
        )
        .bind(delivery_id)
        .bind(courier_id)
        .bind(revision.as_str())
        .execute(connection))?;
        Ok(updated.rows_affected() > 0)
    }
}

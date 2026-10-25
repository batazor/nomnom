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

use std::future::Future;
use std::sync::Arc;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::time::Duration;

use sqlx::Postgres;
use sqlx::pool::PoolConnection;
use sqlx::postgres::{PgPool, PgPoolOptions};
use tokio::runtime::{Handle, Runtime, RuntimeFlavor};

pub use migrate::{MIGRATOR, migrate};
pub use outbox::{NoIntegrationEvents, OUTBOX_TOPIC, OutboxMapper, OutboxRecord, event_id};

use crate::pkg::config::{DATABASE_URL_ENV, DatabaseConfig};
use crate::pkg::persistence::RepositoryError;
use transaction::{put_active, take_active};

/// How long a caller waits for a free connection.
const ACQUIRE_TIMEOUT: Duration = Duration::from_secs(5);

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

// The port implementations use `sql!`, so they are declared after it.
mod outbox;
mod read_models;
mod repositories;
mod rows;
mod transaction;

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

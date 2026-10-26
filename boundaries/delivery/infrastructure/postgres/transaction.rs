//! Unit of Work of [`PostgresStore`]: the connection of the running
//! transaction lives in a thread-local slot keyed by the store.
//!
//! Metrics (uow section of the inventory ops/grafana/inventory; registry
//! ops/semconv/model/uow.yaml): every transaction of `run` ends with one
//! `nomnom.uow.transactions` and one `nomnom.uow.duration` (from the lease of
//! its connection to the end of `COMMIT` or `ROLLBACK`) with `nomnom.outcome`:
//! - `committed` — `COMMIT` succeeded;
//! - `rolled_back` — the work failed or panicked and the transaction was
//!   rolled back (a failed `ROLLBACK` closes the connection, the server rolls
//!   back);
//! - `commit_failed` — the transaction could not begin or `COMMIT` failed,
//!   including an unknown commit outcome.
//!
//! A nested `run` is refused before it starts a transaction and records
//! nothing. `run` does not repeat transactions, so `nomnom.uow.retries` has
//! no source here.

use std::cell::RefCell;
use std::time::Instant;

use opentelemetry::KeyValue;
use opentelemetry::metrics::{Counter, Histogram, Meter};

use crate::pkg::application::{UnitOfWork, UnitOfWorkError};
use crate::pkg::telemetry::SECONDS_BUCKETS;

use super::{Connection, PostgresStore};

/// Instrumentation scope of the Unit of Work metrics.
pub const UOW_METER_NAME: &str = "nomnom.delivery.uow";

/// How one transaction ended (`nomnom.outcome`).
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
enum Outcome {
    Committed,
    RolledBack,
    CommitFailed,
}

impl Outcome {
    fn as_str(self) -> &'static str {
        match self {
            Self::Committed => "committed",
            Self::RolledBack => "rolled_back",
            Self::CommitFailed => "commit_failed",
        }
    }
}

/// Unit of Work instruments of one process.
#[derive(Clone)]
pub struct TransactionMetrics {
    transactions: Counter<u64>,
    duration: Histogram<f64>,
}

impl TransactionMetrics {
    /// Instruments of the global MeterProvider (no-op until the pipeline
    /// starts).
    #[must_use]
    pub fn global() -> Self {
        Self::new(&opentelemetry::global::meter(UOW_METER_NAME))
    }

    #[must_use]
    pub fn new(meter: &Meter) -> Self {
        Self {
            transactions: meter
                .u64_counter("nomnom.uow.transactions")
                .with_unit("{transaction}")
                .with_description("Unit of Work transactions by outcome.")
                .build(),
            duration: meter
                .f64_histogram("nomnom.uow.duration")
                .with_unit("s")
                .with_description("Duration of one Unit of Work transaction.")
                .with_boundaries(SECONDS_BUCKETS.to_vec())
                .build(),
        }
    }

    fn record(&self, outcome: Outcome, started: Instant) {
        let attributes = [KeyValue::new("nomnom.outcome", outcome.as_str())];
        self.transactions.add(1, &attributes);
        self.duration
            .record(started.elapsed().as_secs_f64(), &attributes);
    }
}

thread_local! {
    /// Connections of transactions running on this thread, by store id.
    static ACTIVE: RefCell<Vec<(usize, Connection)>> = const { RefCell::new(Vec::new()) };
}

pub(super) fn take_active(store: usize) -> Option<Connection> {
    ACTIVE.with(|active| {
        let mut active = active.borrow_mut();
        let index = active.iter().position(|(id, _)| *id == store)?;
        Some(active.swap_remove(index).1)
    })
}

pub(super) fn put_active(store: usize, connection: Connection) {
    ACTIVE.with(|active| active.borrow_mut().push((store, connection)));
}

/// Guard of a running transaction: a panic inside the work drops the
/// connection (closed, rolled back by the server) instead of leaking it.
struct ActiveTransaction<'a> {
    store: &'a PostgresStore,
    started: Instant,
    finished: bool,
}

impl Drop for ActiveTransaction<'_> {
    fn drop(&mut self) {
        if self.finished {
            return;
        }
        if let Some(connection) = take_active(self.store.id) {
            self.store.discard_connection(connection);
        }
        self.store
            .transactions
            .record(Outcome::RolledBack, self.started);
    }
}

impl PostgresStore {
    /// Runs a transaction control statement and records its duration.
    fn control(
        &self,
        connection: &mut sqlx::PgConnection,
        statement: &'static str,
    ) -> Result<(), sqlx::Error> {
        let span = crate::pkg::telemetry::db::start(
            &self.tracer,
            &crate::pkg::telemetry::db::Statement::of_sql(statement),
        );
        let started = std::time::Instant::now();
        let result = self
            .block_on(async move { sqlx::raw_sql(statement).execute(connection).await.map(drop) });
        crate::pkg::telemetry::db::end(span, &result);
        self.metrics.operation(statement, None, started, &result);
        result
    }
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
        let started = Instant::now();
        let not_begun = |reason: String| {
            self.transactions.record(Outcome::CommitFailed, started);
            begin_failed(reason)
        };
        let mut lease = self.lease().map_err(|error| not_begun(error.to_string()))?;
        if let Err(error) = self.control(&mut lease.connection, "BEGIN") {
            self.drop_connection(lease.connection);
            return Err(not_begun(error.to_string()));
        }
        put_active(self.id, lease.connection);
        let mut guard = ActiveTransaction {
            store: self,
            started,
            finished: false,
        };
        let result = work();
        guard.finished = true;
        let Some(mut connection) = take_active(self.id) else {
            self.transactions.record(Outcome::CommitFailed, started);
            return Err(E::from(UnitOfWorkError::CommitOutcomeUnknown));
        };
        match result {
            Ok(value) => match self.control(&mut connection, "COMMIT") {
                Ok(()) => {
                    self.drop_connection(connection);
                    self.transactions.record(Outcome::Committed, started);
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
                    self.transactions.record(Outcome::CommitFailed, started);
                    Err(E::from(outcome))
                }
            },
            Err(error) => {
                match self.control(&mut connection, "ROLLBACK") {
                    Ok(()) => self.drop_connection(connection),
                    Err(_) => self.discard_connection(connection),
                }
                self.transactions.record(Outcome::RolledBack, started);
                Err(error)
            }
        }
    }
}

#[cfg(test)]
#[path = "transaction/tests.rs"]
mod tests;

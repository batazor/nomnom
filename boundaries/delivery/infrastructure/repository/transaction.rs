//! Unit of Work of [`PostgresStore`]: the connection of the running
//! transaction lives in a thread-local slot keyed by the store.

use std::cell::RefCell;

use crate::pkg::application::{UnitOfWork, UnitOfWorkError};

use super::{Connection, PostgresStore};

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
        let mut lease = self
            .lease()
            .map_err(|error| begin_failed(error.to_string()))?;
        if let Err(error) = self.control(&mut lease.connection, "BEGIN") {
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
            Ok(value) => match self.control(&mut connection, "COMMIT") {
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
                match self.control(&mut connection, "ROLLBACK") {
                    Ok(()) => self.drop_connection(connection),
                    Err(_) => self.discard_connection(connection),
                }
                Err(error)
            }
        }
    }
}

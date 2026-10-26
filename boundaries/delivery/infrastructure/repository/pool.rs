//! Connection pool of the `delivery` processes: limits from
//! [`database::Config`] and OTel semconv database client metrics with
//! `db.client.connection.pool.name` (the same names as `orders`, `payments`
//! and `restaurants`; the process is told apart by `service.name`).
//!
//! - `db.client.connection.count` (`state` = `idle` | `used`),
//!   `db.client.connection.max`, `db.client.connection.idle.min` — observed
//!   from the pool at collection;
//! - `db.client.connection.pending_requests`, `db.client.connection.timeouts`
//!   and `db.client.connection.wait_time` — around every acquire;
//! - `db.client.connection.use_time` — from acquire to the return of the
//!   connection (a Unit of Work holds one connection for its transaction);
//! - `db.client.connection.create_time` — from the start of an acquire that
//!   opened a new connection to its `after_connect` (sqlx has no hook before
//!   the connect, so a wait for a free slot before it is included);
//! - `db.client.operation.duration` (`db.system.name`, `db.operation.name`,
//!   `db.collection.name`, `error.type`) — every statement of the store.
//!
//! The pool works behind a session-mode pooler only: the migrations take a
//! session-level advisory lock.

use std::ops::{Deref, DerefMut};
use std::sync::{Arc, Mutex, PoisonError, Weak};
use std::time::Instant;

use opentelemetry::KeyValue;
use opentelemetry::metrics::{Counter, Histogram, Meter, ObservableUpDownCounter, UpDownCounter};
use sqlx::Postgres;
use sqlx::pool::PoolConnection;
use sqlx::postgres::{PgConnection, PgPool, PgPoolOptions};

use crate::pkg::config::database;

/// `db.client.connection.pool.name` of every `delivery` pool.
pub const POOL_NAME: &str = "delivery";

/// Instrumentation scope of the database client metrics.
pub const POOL_METER_NAME: &str = "nomnom.delivery.postgres";

const POOL_NAME_ATTRIBUTE: &str = "db.client.connection.pool.name";
const STATE_ATTRIBUTE: &str = "db.client.connection.state";

/// Acquire from a warm pool takes microseconds, a new connection through the
/// pooler milliseconds, a saturated pool up to the acquire timeout; use time
/// spans a Unit of Work (as `orders`).
pub const POOL_TIME_BUCKETS: [f64; 15] = [
    0.0005, 0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0, 30.0,
];

/// Advisory buckets of `db.client.operation.duration` (semconv).
pub const OPERATION_BUCKETS: [f64; 9] = [0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1.0, 5.0, 10.0];

tokio::task_local! {
    /// Start of the acquire running on this task, for `create_time`.
    static ACQUIRE_STARTED: Instant;
}

/// Pool options of `config`: size, idle minimum, lifetime, idle timeout and
/// acquire timeout. `max_conns` is the size of this pool; the caller may
/// lower it (startup migrations).
#[must_use]
pub fn pool_options(config: &database::Config, max_conns: u32) -> PgPoolOptions {
    PgPoolOptions::new()
        .max_connections(max_conns)
        .min_connections(config.min_conns.min(max_conns))
        .max_lifetime(config.max_lifetime)
        .idle_timeout(config.idle_timeout)
        .acquire_timeout(config.acquire_timeout)
}

/// Points of one observable instrument: `(value, db.client.connection.state)`.
type Points = Vec<(i64, Option<&'static str>)>;
/// Reads the points of one observable instrument from the pool.
type ReadPool = fn(&PgPool, &mut Points);

/// Database client instruments of one process.
#[derive(Clone)]
pub struct PoolMetrics {
    meter: Meter,
    attributes: Arc<[KeyValue; 1]>,
    pending: UpDownCounter<i64>,
    timeouts: Counter<u64>,
    wait_time: Histogram<f64>,
    use_time: Histogram<f64>,
    create_time: Histogram<f64>,
    operation: Histogram<f64>,
    observed: Arc<Mutex<Vec<ObservableUpDownCounter<i64>>>>,
}

impl PoolMetrics {
    /// Instruments of the global MeterProvider (no-op without telemetry).
    /// The pipeline must be started before: instruments bind at creation.
    #[must_use]
    pub fn global() -> Self {
        Self::new(&opentelemetry::global::meter(POOL_METER_NAME))
    }

    #[must_use]
    pub fn new(meter: &Meter) -> Self {
        Self {
            meter: meter.clone(),
            attributes: Arc::new([KeyValue::new(POOL_NAME_ATTRIBUTE, POOL_NAME)]),
            pending: meter
                .i64_up_down_counter("db.client.connection.pending_requests")
                .with_unit("{request}")
                .with_description("Acquires waiting for a pool connection.")
                .build(),
            timeouts: meter
                .u64_counter("db.client.connection.timeouts")
                .with_unit("{timeout}")
                .with_description("Acquires that got no connection within the acquire timeout.")
                .build(),
            wait_time: seconds(
                meter,
                "db.client.connection.wait_time",
                "Time to obtain a connection from the pool.",
                &POOL_TIME_BUCKETS,
            ),
            use_time: seconds(
                meter,
                "db.client.connection.use_time",
                "Time from acquire to the return of a connection.",
                &POOL_TIME_BUCKETS,
            ),
            create_time: seconds(
                meter,
                "db.client.connection.create_time",
                "Time to open a new connection, handshake and auth included.",
                &POOL_TIME_BUCKETS,
            ),
            operation: seconds(
                meter,
                "db.client.operation.duration",
                "Duration of database client operations.",
                &OPERATION_BUCKETS,
            ),
            observed: Arc::default(),
        }
    }

    /// `options` that record `create_time` of the connections they open.
    #[must_use]
    pub fn instrument(&self, options: PgPoolOptions) -> PgPoolOptions {
        let create_time = self.create_time.clone();
        let attributes = Arc::clone(&self.attributes);
        options.after_connect(move |_connection, _meta| {
            // Background connects (min_conns) run outside an acquire.
            if let Ok(started) = ACQUIRE_STARTED.try_with(|started| *started) {
                create_time.record(started.elapsed().as_secs_f64(), attributes.as_ref());
            }
            Box::pin(async { Ok(()) })
        })
    }

    /// Registers the pool gauges. They read `pool` through a weak reference:
    /// a dropped pool is no longer observed.
    pub fn observe(&self, pool: &Arc<PgPool>) {
        let observe = |name: &'static str, description: &'static str, read: ReadPool| {
            let weak: Weak<PgPool> = Arc::downgrade(pool);
            self.meter
                .i64_observable_up_down_counter(name)
                .with_unit("{connection}")
                .with_description(description)
                .with_callback(move |observer| {
                    let Some(pool) = weak.upgrade() else { return };
                    let mut points = Points::with_capacity(2);
                    read(&pool, &mut points);
                    for (value, state) in points {
                        let pool_name = KeyValue::new(POOL_NAME_ATTRIBUTE, POOL_NAME);
                        match state {
                            Some(state) => observer.observe(
                                value,
                                &[pool_name, KeyValue::new(STATE_ATTRIBUTE, state)],
                            ),
                            None => observer.observe(value, &[pool_name]),
                        }
                    }
                })
                .build()
        };
        let gauges = [
            observe(
                "db.client.connection.count",
                "Open connections of the pool by state.",
                |pool, points| {
                    let size = i64::from(pool.size());
                    let idle = i64::try_from(pool.num_idle()).unwrap_or(i64::MAX);
                    points.push((idle, Some("idle")));
                    points.push(((size - idle).max(0), Some("used")));
                },
            ),
            observe(
                "db.client.connection.max",
                "Maximum open connections of the pool.",
                |pool, points| {
                    points.push((i64::from(pool.options().get_max_connections()), None));
                },
            ),
            observe(
                "db.client.connection.idle.min",
                "Minimum idle connections the pool keeps open.",
                |pool, points| {
                    points.push((i64::from(pool.options().get_min_connections()), None));
                },
            ),
        ];
        self.observed
            .lock()
            .unwrap_or_else(PoisonError::into_inner)
            .extend(gauges);
    }

    /// Acquires a connection of `pool`, recording the pending request, the
    /// wait time and a timeout.
    pub async fn acquire(&self, pool: &PgPool) -> Result<ObservedConnection, sqlx::Error> {
        let started = Instant::now();
        self.pending.add(1, self.attributes.as_ref());
        let result = ACQUIRE_STARTED.scope(started, pool.acquire()).await;
        self.pending.add(-1, self.attributes.as_ref());
        match result {
            Ok(connection) => {
                self.wait_time
                    .record(started.elapsed().as_secs_f64(), self.attributes.as_ref());
                Ok(ObservedConnection {
                    connection: Some(connection),
                    acquired: Instant::now(),
                    metrics: self.clone(),
                })
            }
            Err(error) => {
                if matches!(error, sqlx::Error::PoolTimedOut) {
                    self.timeouts.add(1, self.attributes.as_ref());
                }
                Err(error)
            }
        }
    }

    /// Records one statement started at `started`. `operation` is its SQL
    /// keyword (`SELECT`, `INSERT`, ...); a failure carries `error.type`.
    pub fn operation<T>(
        &self,
        operation: &'static str,
        collection: Option<&str>,
        started: Instant,
        result: &Result<T, sqlx::Error>,
    ) {
        let mut attributes = vec![
            KeyValue::new("db.system.name", "postgresql"),
            KeyValue::new("db.operation.name", operation),
        ];
        if let Some(collection) = collection {
            attributes.push(KeyValue::new("db.collection.name", collection.to_owned()));
        }
        if let Err(error) = result {
            attributes.push(KeyValue::new("error.type", error_type(error)));
        }
        self.operation
            .record(started.elapsed().as_secs_f64(), &attributes);
    }
}

fn seconds(
    meter: &Meter,
    name: &'static str,
    description: &'static str,
    buckets: &[f64],
) -> Histogram<f64> {
    meter
        .f64_histogram(name)
        .with_unit("s")
        .with_description(description)
        .with_boundaries(buckets.to_vec())
        .build()
}

pub use crate::pkg::telemetry::db::error_type;

/// SQL keyword of `statement` for `db.operation.name` (`_OTHER` if none).
#[must_use]
pub fn operation_name(statement: &str) -> &'static str {
    let keyword = statement
        .trim_start()
        .split(|c: char| !c.is_ascii_alphabetic())
        .next()
        .unwrap_or_default();
    for known in [
        "SELECT", "INSERT", "UPDATE", "DELETE", "BEGIN", "COMMIT", "ROLLBACK", "WITH",
    ] {
        if keyword.eq_ignore_ascii_case(known) {
            return known;
        }
    }
    "_OTHER"
}

/// Pooled connection that records its use time when returned.
pub struct ObservedConnection {
    connection: Option<PoolConnection<Postgres>>,
    acquired: Instant,
    metrics: PoolMetrics,
}

impl ObservedConnection {
    /// Closes the connection instead of returning it to the pool.
    pub fn close_on_drop(&mut self) {
        if let Some(connection) = self.connection.as_mut() {
            connection.close_on_drop();
        }
    }
}

impl Deref for ObservedConnection {
    type Target = PgConnection;

    fn deref(&self) -> &PgConnection {
        self.connection.as_ref().expect("connection until drop")
    }
}

impl DerefMut for ObservedConnection {
    fn deref_mut(&mut self) -> &mut PgConnection {
        self.connection.as_mut().expect("connection until drop")
    }
}

impl Drop for ObservedConnection {
    fn drop(&mut self) {
        self.metrics.use_time.record(
            self.acquired.elapsed().as_secs_f64(),
            self.metrics.attributes.as_ref(),
        );
        drop(self.connection.take());
    }
}

#[cfg(test)]
mod tests;

//! Containers of the integration tests (testcontainers): one migrated
//! PostgreSQL per test binary. Tests use unique ids, so they share the
//! database. Containers live in statics, which are never dropped, so they are
//! removed when the test binary exits.

#![allow(dead_code)]

use std::any::Any;
use std::sync::{Mutex, OnceLock, PoisonError};

use delivery::infrastructure::postgres::migrate;
use delivery::pkg::config::DatabaseConfig;
use testcontainers_modules::postgres::Postgres;
use testcontainers_modules::testcontainers::runners::SyncRunner;
use testcontainers_modules::testcontainers::{Container, Image, ImageExt};

/// Image of the platform PostgreSQL (ops).
const POSTGRES_TAG: &str = "18.6-alpine3.24";

static CONTAINERS: Mutex<Vec<Box<dyn Any + Send>>> = Mutex::new(Vec::new());
static DATABASE: OnceLock<String> = OnceLock::new();

extern "C" fn remove_containers() {
    CONTAINERS
        .lock()
        .unwrap_or_else(PoisonError::into_inner)
        .clear();
}

/// Keeps the container until the test binary exits.
pub fn keep<I: Image + 'static>(container: Container<I>) {
    let mut containers = CONTAINERS.lock().unwrap_or_else(PoisonError::into_inner);
    if containers.is_empty() {
        // SAFETY: registers a plain `extern "C"` function without arguments.
        unsafe { libc::atexit(remove_containers) };
    }
    containers.push(Box::new(container));
}

/// Runs container setup off the async runtime of `#[tokio::test]`: the sync
/// runner blocks on its own runtime.
pub fn off_runtime<T: Send>(work: impl FnOnce() -> T + Send) -> T {
    std::thread::scope(|scope| scope.spawn(work).join().expect("container setup"))
}

/// Migrated database of the shared PostgreSQL container.
pub fn database() -> DatabaseConfig {
    let url = DATABASE.get_or_init(|| {
        off_runtime(|| {
            let container = Postgres::default()
                .with_tag(POSTGRES_TAG)
                .with_cmd(["postgres", "-c", "max_connections=500"])
                .start()
                .expect("start postgres container");
            let host = container.get_host().expect("container host");
            let port = container.get_host_port_ipv4(5432).expect("container port");
            keep(container);
            let url = format!("postgres://postgres:postgres@{host}:{port}/postgres");
            migrate(&DatabaseConfig {
                url: url.clone(),
                ..DatabaseConfig::default()
            })
            .expect("migrate");
            url
        })
    });
    DatabaseConfig {
        url: url.clone(),
        max_conns: 4,
        ..DatabaseConfig::default()
    }
}

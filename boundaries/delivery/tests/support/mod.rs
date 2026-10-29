//! Containers of the integration tests (testcontainers): one migrated
//! PostgreSQL and one SpiceDB per test binary. Tests use unique ids, so they share the
//! database. Containers live in statics, which are never dropped, so they are
//! removed when the test binary exits.

#![allow(dead_code)]

use std::any::Any;
use std::path::Path;
use std::sync::{Mutex, OnceLock, PoisonError};
use std::time::Duration;

use delivery::infrastructure::repository::migrate;
use delivery::pkg::config::{SpiceDbConfig, TemporalConfig, database};
use testcontainers_modules::postgres::Postgres;
use testcontainers_modules::testcontainers::core::{IntoContainerPort, WaitFor};
use testcontainers_modules::testcontainers::runners::SyncRunner;
use testcontainers_modules::testcontainers::{Container, GenericImage, Image, ImageExt};

/// Image of the platform PostgreSQL (ops).
const POSTGRES_TAG: &str = "18.6-alpine3.24";
/// Image of the platform SpiceDB (`ops/kind/bootstrap/values.yaml`).
const SPICEDB_IMAGE: &str = "authzed/spicedb";
const SPICEDB_TAG: &str = "v1.57.0";
const SPICEDB_KEY: &str = "devkey";
const SPICEDB_GRPC: u16 = 50051;
/// Loopback, the address of the ports Docker publishes for the tests: an IP literal keeps clients off the DNS of the host (a gRPC client also looks up TXT _grpc_config.<host> for a name, which a slow local resolver answers in seconds).
const LOOPBACK: &str = "127.0.0.1";

static CONTAINERS: Mutex<Vec<Box<dyn Any + Send>>> = Mutex::new(Vec::new());
static DATABASE: OnceLock<String> = OnceLock::new();
static SPICEDB: OnceLock<String> = OnceLock::new();
static TEMPORAL: OnceLock<String> = OnceLock::new();
/// Temporal CLI dev server of `ops/kind/manifests/messaging/infrastructure.yaml`.
const TEMPORAL_IMAGE: &str = "temporalio/temporal";
const TEMPORAL_TAG: &str = "1.9.1";
const TEMPORAL_GRPC: u16 = 7233;

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
pub fn database() -> database::Config {
    pooled(DATABASE.get_or_init(|| off_runtime(start_postgres)))
}

/// Migrated database of a PostgreSQL container of its own, for a test that
/// scans every row (reconciliation) and must not see parallel tests.
pub fn own_database() -> database::Config {
    pooled(&off_runtime(start_postgres))
}

/// New database without schema on the shared PostgreSQL container, for a
/// test of the startup migrations.
pub fn empty_database(name: &str) -> database::Config {
    let server = DATABASE.get_or_init(|| off_runtime(start_postgres));
    let statement = format!("CREATE DATABASE {name}");
    off_runtime(|| {
        tokio::runtime::Builder::new_current_thread()
            .enable_all()
            .build()
            .expect("runtime")
            .block_on(async {
                let pool = sqlx::PgPool::connect(server).await.expect("connect");
                sqlx::raw_sql(sqlx::AssertSqlSafe(statement))
                    .execute(&pool)
                    .await
                    .expect("create database");
                pool.close().await;
            });
    });
    let (base, _) = server.rsplit_once('/').expect("database in uri");
    pooled(&format!("{base}/{name}"))
}

fn pooled(uri: &str) -> database::Config {
    database::Config {
        uri: uri.to_owned(),
        max_conns: 4,
        ..database::Config::default()
    }
}

fn start_postgres() -> String {
    let container = Postgres::default()
        .with_tag(POSTGRES_TAG)
        .with_cmd(["postgres", "-c", "max_connections=500"])
        .start()
        .expect("start postgres container");
    let port = container.get_host_port_ipv4(5432).expect("container port");
    keep(container);
    let uri = format!("postgres://postgres:postgres@{LOOPBACK}:{port}/postgres");
    migrate(&database::Config {
        uri: uri.clone(),
        ..database::Config::default()
    })
    .expect("migrate");
    uri
}

/// SpiceDB of the shared container: in-memory datastore bootstrapped with
/// `authorization/bootstrap.yaml` (schema plus deployment grants).
pub fn spicedb(timeout: Duration) -> SpiceDbConfig {
    let endpoint = SPICEDB.get_or_init(|| {
        off_runtime(|| {
            let authorization = Path::new(env!("CARGO_MANIFEST_DIR")).join("../../authorization");
            let container = GenericImage::new(SPICEDB_IMAGE, SPICEDB_TAG)
                .with_exposed_port(SPICEDB_GRPC.tcp())
                .with_wait_for(WaitFor::message_on_stderr("grpc server started serving"))
                .with_working_dir("/authorization")
                .with_copy_to(
                    "/authorization/bootstrap.yaml",
                    authorization.join("bootstrap.yaml"),
                )
                .with_copy_to(
                    "/authorization/schema.zed",
                    authorization.join("schema.zed"),
                )
                .with_cmd([
                    "serve",
                    "--grpc-preshared-key",
                    SPICEDB_KEY,
                    "--datastore-engine",
                    "memory",
                    "--datastore-bootstrap-files",
                    "bootstrap.yaml",
                ])
                .start()
                .expect("start spicedb container");
            let port = container
                .get_host_port_ipv4(SPICEDB_GRPC)
                .expect("container port");
            keep(container);
            format!("{LOOPBACK}:{port}")
        })
    });
    SpiceDbConfig {
        endpoint: endpoint.clone(),
        preshared_key: SPICEDB_KEY.to_owned(),
        insecure: true,
        timeout,
    }
}

/// Temporal dev server shared by the test binary, namespace `default`.
pub fn temporal() -> TemporalConfig {
    TemporalConfig {
        host_port: TEMPORAL.get_or_init(|| off_runtime(start_temporal)).clone(),
        namespace: "default".to_owned(),
        connect_timeout: Duration::from_secs(10),
    }
}

fn start_temporal() -> String {
    let container = GenericImage::new(TEMPORAL_IMAGE, TEMPORAL_TAG)
        .with_exposed_port(TEMPORAL_GRPC.tcp())
        .with_wait_for(WaitFor::message_on_stdout("Server:"))
        .with_cmd([
            "server",
            "start-dev",
            "--ip",
            "0.0.0.0",
            "--namespace",
            "default",
            "--headless",
        ])
        .start()
        .expect("start temporal");
    let port = container
        .get_host_port_ipv4(TEMPORAL_GRPC.tcp())
        .expect("temporal port");
    keep(container);
    format!("{LOOPBACK}:{port}")
}

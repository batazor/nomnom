//! Schema migrations of `delivery` (`sqlx::migrate!`, ADR 31), embedded in
//! the binary. The tables are split into migration sets by the data they
//! store; each set has its own sqlx history table: `messaging` (the shared
//! Outbox, migrations next to its repository code in
//! `infrastructure/repository`), `couriers`, `deliveries` and `simulator`
//! (migrations in `slices/<slice>/infrastructure/repository/migrations`). A set
//! touches only its own tables, and no table references another set's table.
//!
//! The `delivery` process applies every set at startup, before it opens its
//! pool and ports (`di::store`): an up-to-date schema is a no-op, a failure
//! stops the process. A session advisory lock serializes concurrent starts of
//! several replicas; it is taken by `migrate` rather than sqlx, because the
//! wait for it must not be cut by `lock_timeout` of the DDL.

use std::borrow::Cow;

use sqlx::migrate::{Migrate, Migrator};

use crate::pkg::config::{DATABASE_URL_ENV, DatabaseConfig};

/// Migrations of one set of tables.
pub struct MigrationSet {
    /// Storage id of the set: part of the database schema contract, it names
    /// the history table `_sqlx_migrations_<id>`. Chosen by the stored data,
    /// never derived from module, directory or slice names, and never changed
    /// by a code refactoring; changing it requires a migration of the history
    /// table.
    pub id: &'static str,
    /// Migrations embedded at compile time; append-only. History in
    /// `_sqlx_migrations_<id>`. Without the lock of sqlx: `migrate` holds
    /// its own (`locking` and `table_name` are public fields `migrate!` fills,
    /// and their setters are not `const`).
    pub migrator: Migrator,
}

/// Set with storage id `$id` over the migrations in `$path`: the history table
/// name is built from the id alone.
macro_rules! migration_set {
    ($id:literal, $path:literal) => {
        MigrationSet {
            id: $id,
            migrator: Migrator {
                locking: false,
                table_name: Cow::Borrowed(concat!("_sqlx_migrations_", $id)),
                ..sqlx::migrate!($path)
            },
        }
    };
}

/// Every migration set of the process, in the order `migrate` applies them:
/// the shared `messaging` set first, then the sets of the slices. The ids are
/// listed again in `SQLX_MIGRATION_SETS` of the Makefile.
pub static MIGRATION_SETS: [MigrationSet; 4] = [
    migration_set!("messaging", "infrastructure/repository/migrations"),
    migration_set!(
        "couriers",
        "slices/courier/infrastructure/repository/migrations"
    ),
    migration_set!(
        "deliveries",
        "slices/delivery/infrastructure/repository/migrations"
    ),
    migration_set!(
        "simulator",
        "slices/simulator/infrastructure/repository/migrations"
    ),
];

/// Pool size of the startup migration, whatever `DATABASE__MAX_CONNS` says.
pub const MIGRATE_MAX_CONNS: u32 = 1;

/// Arbitrary constant shared by every delivery migrator.
const LOCK_KEY: i64 = 0x6465_6C69_7665_7279; // "delivery"

/// Bound of every lock wait of a migration. Migrations run during a rolling
/// update next to live traffic: an `ALTER TABLE` queued behind a long
/// transaction would stall every query of the table behind it, so it fails
/// fast and the pod retries on restart (each migration is its own transaction).
const LOCK_TIMEOUT: &str = "SET lock_timeout = '5s'";

/// Applies pending migrations of every set; returns `(id, version)` of the
/// migrations applied by this run, in the order they were applied.
pub fn migrate(config: &DatabaseConfig) -> Result<Vec<(&'static str, i64)>, String> {
    if !config.enabled() {
        return Err(format!("{DATABASE_URL_ENV} is required"));
    }
    let url = config.url.clone();
    // One connection: the migrator holds a session-level advisory lock on it.
    let options = super::pool_options(config, MIGRATE_MAX_CONNS);
    super::blocking(move || {
        let runtime = tokio::runtime::Builder::new_current_thread()
            .enable_all()
            .build()
            .map_err(|error| format!("migration runtime: {error}"))?;
        runtime.block_on(async move {
            let pool = options
                .connect(&url)
                .await
                .map_err(|error| format!("connect database: {error}"))?;
            let mut connection = pool
                .acquire()
                .await
                .map_err(|error| format!("connect database: {error}"))?;
            // Waits for another replica as long as it migrates; set before
            // lock_timeout, which would bound this wait too.
            sqlx::query!("SELECT pg_advisory_lock($1)", LOCK_KEY)
                .execute(&mut *connection)
                .await
                .map_err(|error| format!("migration lock: {error}"))?;
            sqlx::raw_sql(LOCK_TIMEOUT)
                .execute(&mut *connection)
                .await
                .map_err(|error| format!("migration lock_timeout: {error}"))?;
            let mut applied = Vec::new();
            for set in &MIGRATION_SETS {
                let before = applied_versions(&mut connection, set).await?;
                set.migrator
                    .run(&mut *connection)
                    .await
                    .map_err(|error| format!("migrate {}: {error}", set.id))?;
                let after = applied_versions(&mut connection, set).await?;
                applied.extend(
                    after
                        .into_iter()
                        .filter(|version| !before.contains(version))
                        .map(|version| (set.id, version)),
                );
            }
            // Closing the connection releases the advisory lock with the session.
            connection
                .close()
                .await
                .map_err(|error| format!("close database: {error}"))?;
            pool.close().await;
            Ok(applied)
        })
    })
}

/// Versions in the history table of `set`, created empty before the first run.
async fn applied_versions(
    connection: &mut sqlx::PgConnection,
    set: &MigrationSet,
) -> Result<Vec<i64>, String> {
    let table = &set.migrator.table_name;
    connection
        .ensure_migrations_table(table)
        .await
        .map_err(|error| format!("migration history {}: {error}", set.id))?;
    let applied = connection
        .list_applied_migrations(table)
        .await
        .map_err(|error| format!("migration history {}: {error}", set.id))?;
    Ok(applied
        .into_iter()
        .map(|migration| migration.version)
        .collect())
}

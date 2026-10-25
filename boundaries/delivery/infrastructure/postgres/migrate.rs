//! Schema migrations of `delivery` (`sqlx::migrate!`, ADR 31), embedded in
//! the binary. The `delivery` process applies them at startup, before it opens
//! its pool and ports (`di::postgres`): an up-to-date schema is a no-op, a
//! failure stops the process. A session advisory lock serializes concurrent
//! starts of several replicas; it is taken by `migrate` rather than sqlx,
//! because the wait for it must not be cut by `lock_timeout` of the DDL.

use sqlx::migrate::Migrator;

use crate::pkg::config::{DATABASE_URL_ENV, DatabaseConfig};

/// Migrations embedded at compile time; append-only. Without the lock of sqlx:
/// `migrate` holds its own (`locking` is a public field `migrate!` fills, and
/// `set_locking` is not `const`).
pub static MIGRATOR: Migrator = Migrator {
    locking: false,
    ..sqlx::migrate!("infrastructure/postgres/migrations")
};

/// Pool size of the startup migration, whatever `DATABASE__MAX_CONNS` says.
pub const MIGRATE_MAX_CONNS: u32 = 1;

/// Arbitrary constant shared by every delivery migrator.
const LOCK_KEY: i64 = 0x6465_6C69_7665_7279; // "delivery"

/// Bound of every lock wait of a migration. Migrations run during a rolling
/// update next to live traffic: an `ALTER TABLE` queued behind a long
/// transaction would stall every query of the table behind it, so it fails
/// fast and the pod retries on restart (each migration is its own transaction).
const LOCK_TIMEOUT: &str = "SET lock_timeout = '5s'";

/// Applies pending migrations; returns the versions applied by this run.
pub fn migrate(config: &DatabaseConfig) -> Result<Vec<i64>, String> {
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
                .map_err(|error| format!("connect postgres: {error}"))?;
            let mut connection = pool
                .acquire()
                .await
                .map_err(|error| format!("connect postgres: {error}"))?;
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
            let before = applied_versions(&mut connection).await?;
            MIGRATOR
                .run(&mut *connection)
                .await
                .map_err(|error| format!("migrate: {error}"))?;
            let after = applied_versions(&mut connection).await?;
            // Closing the connection releases the advisory lock with the session.
            connection
                .close()
                .await
                .map_err(|error| format!("close postgres: {error}"))?;
            pool.close().await;
            Ok(after
                .into_iter()
                .filter(|version| !before.contains(version))
                .collect())
        })
    })
}

/// Versions in sqlx's own `_sqlx_migrations` (checked against the migrated
/// schema of the offline cache); before the first run the table is absent.
async fn applied_versions(connection: &mut sqlx::PgConnection) -> Result<Vec<i64>, String> {
    let exists =
        sqlx::query_scalar!(r#"SELECT to_regclass('_sqlx_migrations') IS NOT NULL AS "exists!""#)
            .fetch_one(&mut *connection)
            .await
            .map_err(|error| format!("migration history: {error}"))?;
    if !exists {
        return Ok(Vec::new());
    }
    sqlx::query_scalar!("SELECT version FROM _sqlx_migrations WHERE success")
        .fetch_all(&mut *connection)
        .await
        .map_err(|error| format!("migration history: {error}"))
}

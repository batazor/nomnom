//! Schema migrations of `delivery` (`sqlx::migrate!`, ADR 31). The
//! `delivery-migrate` process applies them and exits; in Kubernetes it runs as
//! the init container of every pod. sqlx takes an advisory lock, so
//! concurrent runs of several replicas are serialized.

use sqlx::migrate::Migrator;
use sqlx::postgres::PgPoolOptions;

use crate::pkg::config::{DATABASE_URL_ENV, DatabaseConfig};

/// Migrations embedded at compile time; append-only.
pub static MIGRATOR: Migrator = sqlx::migrate!("infrastructure/postgres/migrations");

/// Applies pending migrations; returns the versions applied by this run.
pub fn migrate(config: &DatabaseConfig) -> Result<Vec<i64>, String> {
    if !config.enabled() {
        return Err(format!("{DATABASE_URL_ENV} is required"));
    }
    let url = config.url.clone();
    super::blocking(move || {
        let runtime = tokio::runtime::Builder::new_current_thread()
            .enable_all()
            .build()
            .map_err(|error| format!("migration runtime: {error}"))?;
        runtime.block_on(async move {
            let pool = PgPoolOptions::new()
                .max_connections(1)
                .connect(&url)
                .await
                .map_err(|error| format!("connect postgres: {error}"))?;
            let before: Vec<i64> = applied_versions(&pool).await;
            MIGRATOR
                .run(&pool)
                .await
                .map_err(|error| format!("migrate: {error}"))?;
            let after = applied_versions(&pool).await;
            pool.close().await;
            Ok(after
                .into_iter()
                .filter(|version| !before.contains(version))
                .collect())
        })
    })
}

async fn applied_versions(pool: &sqlx::PgPool) -> Vec<i64> {
    sqlx::query_scalar("SELECT version FROM _sqlx_migrations WHERE success")
        .fetch_all(pool)
        .await
        .unwrap_or_default()
}

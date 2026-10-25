//! `delivery-migrate` применяет миграции PostgreSQL `delivery` и завершается
//! (ADR 31). В Kubernetes запускается init container'ом каждого pod; advisory
//! lock сериализует параллельные запуски реплик.

use std::process::ExitCode;

use delivery::di;
use delivery::pkg::config;
use delivery::pkg::telemetry::log;

fn main() -> ExitCode {
    log::init_logs("delivery-migrate");
    let applied = config::load()
        .map_err(|error| format!("load config: {error}"))
        .and_then(|config| di::migrate::compose(config.database).migrate());
    match applied {
        Ok(applied) => {
            tracing::info!(operation = "migrate", outcome = "success", applied = ?applied, "migrations applied");
            ExitCode::SUCCESS
        }
        Err(error) => {
            tracing::error!(operation = "migrate", outcome = "error", error.message = %error, "migrations failed");
            ExitCode::FAILURE
        }
    }
}

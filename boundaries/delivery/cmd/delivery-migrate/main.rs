//! `delivery-migrate` применяет миграции PostgreSQL `delivery` и завершается
//! (ADR 31). В Kubernetes запускается init container'ом каждого pod; advisory
//! lock сериализует параллельные запуски реплик.

use std::process::ExitCode;

use delivery::di;
use delivery::pkg::config;

fn main() -> ExitCode {
    let applied = config::load()
        .map_err(|error| format!("load config: {error}"))
        .and_then(|config| di::migrate::compose(config.database).migrate());
    match applied {
        Ok(applied) => {
            println!("delivery-migrate: applied {applied:?}");
            ExitCode::SUCCESS
        }
        Err(error) => {
            eprintln!("delivery-migrate: {error}");
            ExitCode::FAILURE
        }
    }
}

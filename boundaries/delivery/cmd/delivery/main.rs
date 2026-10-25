use std::error::Error;
use std::net::SocketAddr;
use std::process::ExitCode;

use delivery::di;
use delivery::di::server::SERVICE_NAME;
use delivery::pkg::config;
use delivery::pkg::lifecycle::{Lifecycle, Settings};
use delivery::pkg::telemetry::log;

#[tokio::main]
async fn main() -> ExitCode {
    // Логи — JSON с первой записи, ошибки конфигурации тоже.
    log::init_logs(SERVICE_NAME);
    match run().await {
        Ok(()) => ExitCode::SUCCESS,
        Err(error) => {
            tracing::error!(operation = "startup", error.message = %error, "delivery stopped with an error");
            ExitCode::FAILURE
        }
    }
}

/// `Ok(())` — штатный shutdown (exit 0), любая ошибка — non-zero exit.
async fn run() -> Result<(), Box<dyn Error>> {
    // Невалидная конфигурация завершает процесс до открытия портов.
    let config = config::load().map_err(|error| format!("load config: {error}"))?;
    // Identity, SpiceDB и PostgreSQL обязательны: ошибка прерывает startup.
    let process = di::server::compose(&config)?;

    let lifecycle = Lifecycle::bind(Settings {
        management_addr: SocketAddr::from(([0, 0, 0, 0], config.management.port)),
        readiness_delay: config.shutdown.readiness_delay,
        drain_timeout: config.shutdown.drain_timeout,
    })
    .await
    .map_err(|error| format!("listen management :{}: {error}", config.management.port))?;

    // Бизнес-порт открывается после технического.
    let worker = process.worker()?;
    let result = lifecycle.run(worker).await;
    // Метрики выгружаются и после ошибки worker.
    process.shutdown();
    result?;
    Ok(())
}

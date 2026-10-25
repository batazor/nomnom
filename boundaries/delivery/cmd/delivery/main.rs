use std::error::Error;
use std::net::SocketAddr;
use std::process::ExitCode;

use delivery::di;
use delivery::pkg::config;
use delivery::pkg::lifecycle::{Lifecycle, Settings};

#[tokio::main]
async fn main() -> ExitCode {
    match run().await {
        Ok(()) => ExitCode::SUCCESS,
        Err(error) => {
            eprintln!("delivery: {error}");
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

use std::error::Error;
use std::net::SocketAddr;
use std::process::ExitCode;
use std::sync::Arc;

use delivery::di;
use delivery::infrastructure::grpc::{GrpcWorker, RejectingSubjectResolver};
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
    let module = Arc::new(di::build());

    let lifecycle = Lifecycle::bind(Settings {
        management_addr: SocketAddr::from(([0, 0, 0, 0], config.management.port)),
        readiness_delay: config.shutdown.readiness_delay,
        drain_timeout: config.shutdown.drain_timeout,
    })
    .await
    .map_err(|error| format!("listen management :{}: {error}", config.management.port))?;

    let worker = GrpcWorker::bind(
        SocketAddr::from(([0, 0, 0, 0], config.server.port)),
        module,
        Arc::new(RejectingSubjectResolver),
    )
    .map_err(|error| format!("listen :{}: {error}", config.server.port))?;

    lifecycle.run(worker).await?;
    Ok(())
}

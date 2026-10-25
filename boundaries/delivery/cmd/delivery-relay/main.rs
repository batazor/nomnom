//! `delivery-relay` публикует Integration Events из `delivery_outbox` в Kafka
//! (ADR 13, ADR 31). Процесс того же образа, что `delivery`, с тем же runtime
//! contract (ADR 17): технический порт с `/health/*`, `SIGTERM`/`SIGINT`, drain.
//! Активна одна реплика — владелец advisory lock; остальные ждут failover.

use std::error::Error;
use std::net::SocketAddr;
use std::process::ExitCode;

use delivery::di::relay::{self, SERVICE_NAME};
use delivery::pkg::config;
use delivery::pkg::lifecycle::{Lifecycle, Settings};

#[tokio::main]
async fn main() -> ExitCode {
    match run().await {
        Ok(()) => ExitCode::SUCCESS,
        Err(error) => {
            eprintln!("{SERVICE_NAME}: {error}");
            ExitCode::FAILURE
        }
    }
}

async fn run() -> Result<(), Box<dyn Error>> {
    let config = config::load().map_err(|error| format!("load config: {error}"))?;
    // Kafka и PostgreSQL обязательны: пустой KAFKA__BROKERS или DATABASE__URL
    // завершают startup до открытия портов.
    let process = relay::compose(&config.database, &config.kafka, config.outbox.clone()).await?;

    let lifecycle = Lifecycle::bind(Settings {
        management_addr: SocketAddr::from(([0, 0, 0, 0], config.management.port)),
        readiness_delay: config.shutdown.readiness_delay,
        drain_timeout: config.shutdown.drain_timeout,
    })
    .await
    .map_err(|error| format!("listen management :{}: {error}", config.management.port))?;

    println!(
        "{SERVICE_NAME}: brokers {}, poll {:?}, batch {}, retention {:?}, metrics export {}",
        config.kafka.brokers,
        config.outbox.poll_interval,
        config.outbox.batch_size,
        config.outbox.retention,
        process.telemetry().exporting()
    );
    let result = lifecycle.run(process.worker()?).await;
    process.shutdown();
    result?;
    Ok(())
}

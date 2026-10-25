//! `delivery-relay` публикует Integration Events из `delivery_outbox` в Kafka
//! (ADR 13, ADR 31). Процесс того же образа, что `delivery`, с тем же runtime
//! contract (ADR 17): технический порт с `/health/*`, `SIGTERM`/`SIGINT`, drain.
//! Активна одна реплика — владелец advisory lock; остальные ждут failover.

use std::error::Error;
use std::net::SocketAddr;
use std::process::ExitCode;
use std::sync::Arc;

use delivery::infrastructure::outbox::{KafkaPublisher, PgOutbox, RelayMetrics, RelayWorker};
use delivery::pkg::config;
use delivery::pkg::lifecycle::{Lifecycle, Settings};
use delivery::pkg::telemetry;

const SERVICE_NAME: &str = "delivery-relay";

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
    config.kafka.require()?;
    let publisher = Arc::new(KafkaPublisher::new(
        &config.kafka,
        config.outbox.publish_timeout,
    )?);
    let outbox = Arc::new(PgOutbox::connect(&config.database).await?);
    let telemetry = telemetry::start(SERVICE_NAME)?;

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
        telemetry.exporting()
    );
    let worker = RelayWorker::new(
        outbox,
        Arc::clone(&publisher),
        config.outbox,
        RelayMetrics::global(),
    );
    let result = lifecycle.run(worker).await;
    publisher.flush();
    telemetry.shutdown();
    result?;
    Ok(())
}

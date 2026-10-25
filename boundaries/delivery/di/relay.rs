//! Composition root процесса `delivery-relay` (`cmd/delivery-relay`):
//! публикация Integration Events из `delivery_outbox` в Kafka (ADR 13, ADR 31).
//!
//! [`compose`] открывает общие клиенты процесса (Kafka producer, пул
//! PostgreSQL relay, metrics pipeline) и передаёт их компонентам
//! [`RelayModule`]; [`RelayWorkerProvider`] собирает из них worker. Kafka и
//! PostgreSQL обязательны: пустой `KAFKA__BROKERS` или `DATABASE__URL`
//! завершают startup до открытия портов.
//!
//! Время жизни: producer, пул и MeterProvider живут весь процесс;
//! [`RelayModule::shutdown`] после остановки worker выгружает сообщения в
//! полёте и накопленные метрики. Advisory lock держит worker.

use std::error::Error;
use std::sync::{Arc, Mutex};

use shaku::{Component, HasComponent, HasProvider, Interface, Module, Provider, module};

use crate::infrastructure::outbox::{KafkaPublisher, PgOutbox, RelayMetrics, RelayWorker};
use crate::pkg::config::{DatabaseConfig, KafkaConfig, OutboxConfig};
use crate::pkg::telemetry::{self, Telemetry};

/// `service.name` процесса и префикс его сообщений.
pub const SERVICE_NAME: &str = "delivery-relay";

/// `delivery_outbox` relay и advisory lock единственной активной реплики.
pub trait RelayOutbox: Interface {
    fn outbox(&self) -> Arc<PgOutbox>;
}

#[derive(Component)]
#[shaku(interface = RelayOutbox)]
pub struct PostgresRelayOutbox {
    outbox: Arc<PgOutbox>,
}

impl RelayOutbox for PostgresRelayOutbox {
    fn outbox(&self) -> Arc<PgOutbox> {
        Arc::clone(&self.outbox)
    }
}

/// Kafka producer процесса.
pub trait RelayPublisher: Interface {
    fn publisher(&self) -> Arc<KafkaPublisher>;

    /// Ждёт сообщения в полёте.
    fn flush(&self);
}

#[derive(Component)]
#[shaku(interface = RelayPublisher)]
pub struct KafkaRelayPublisher {
    publisher: Arc<KafkaPublisher>,
}

impl RelayPublisher for KafkaRelayPublisher {
    fn publisher(&self) -> Arc<KafkaPublisher> {
        Arc::clone(&self.publisher)
    }

    fn flush(&self) {
        self.publisher.flush();
    }
}

/// Metrics pipeline процесса (`pkg/telemetry`).
pub trait RelayTelemetry: Interface {
    /// Экспорт включён.
    fn exporting(&self) -> bool;

    /// Выгружает накопленные точки; повторный вызов ничего не делает.
    fn shutdown(&self);
}

#[derive(Component)]
#[shaku(interface = RelayTelemetry)]
pub struct OtlpTelemetry {
    /// `None` после shutdown.
    telemetry: Mutex<Option<Telemetry>>,
}

impl RelayTelemetry for OtlpTelemetry {
    fn exporting(&self) -> bool {
        self.telemetry
            .lock()
            .is_ok_and(|telemetry| telemetry.as_ref().is_some_and(Telemetry::exporting))
    }

    fn shutdown(&self) {
        if let Some(telemetry) = self
            .telemetry
            .lock()
            .ok()
            .and_then(|mut telemetry| telemetry.take())
        {
            telemetry.shutdown();
        }
    }
}

/// Параметры цикла relay (`OUTBOX__*`).
pub trait RelaySettings: Interface {
    fn outbox(&self) -> &OutboxConfig;
}

#[derive(Component)]
#[shaku(interface = RelaySettings)]
pub struct OutboxSettings {
    config: OutboxConfig,
}

impl RelaySettings for OutboxSettings {
    fn outbox(&self) -> &OutboxConfig {
        &self.config
    }
}

/// Worker процесса.
pub type DeliveryRelayWorker = RelayWorker<Arc<KafkaPublisher>>;

/// Создаёт [`DeliveryRelayWorker`]. Instruments берутся у глобального
/// MeterProvider, поэтому worker создаётся после [`compose`], который
/// запускает telemetry.
pub struct RelayWorkerProvider;

impl<M> Provider<M> for RelayWorkerProvider
where
    M: Module
        + HasComponent<dyn RelayOutbox>
        + HasComponent<dyn RelayPublisher>
        + HasComponent<dyn RelaySettings>,
{
    type Interface = DeliveryRelayWorker;

    fn provide(module: &M) -> Result<Box<DeliveryRelayWorker>, Box<dyn Error>> {
        let outbox: &dyn RelayOutbox = module.resolve_ref();
        let publisher: &dyn RelayPublisher = module.resolve_ref();
        let settings: &dyn RelaySettings = module.resolve_ref();
        Ok(Box::new(RelayWorker::new(
            outbox.outbox(),
            publisher.publisher(),
            settings.outbox().clone(),
            RelayMetrics::global(),
        )))
    }
}

module! {
    pub RelayModule {
        components = [PostgresRelayOutbox, KafkaRelayPublisher, OtlpTelemetry, OutboxSettings],
        providers = [RelayWorkerProvider]
    }
}

impl RelayModule {
    #[must_use]
    pub fn telemetry(&self) -> &dyn RelayTelemetry {
        self.resolve_ref()
    }

    pub fn worker(&self) -> Result<DeliveryRelayWorker, Box<dyn Error>> {
        HasProvider::<DeliveryRelayWorker>::provide(self).map(|worker| *worker)
    }

    /// Освобождает клиенты процесса после остановки worker: сначала Kafka
    /// producer дожидается сообщений в полёте, затем выгружаются метрики.
    pub fn shutdown(&self) {
        HasComponent::<dyn RelayPublisher>::resolve_ref(self).flush();
        self.telemetry().shutdown();
    }
}

/// Собирает граф `delivery-relay`. Вызывается внутри tokio runtime: пул
/// PostgreSQL и OTLP exporter держат соединения на нём.
pub async fn compose(
    database: &DatabaseConfig,
    kafka: &KafkaConfig,
    outbox: OutboxConfig,
) -> Result<RelayModule, Box<dyn Error>> {
    kafka.require()?;
    let publisher = Arc::new(KafkaPublisher::new(kafka, outbox.publish_timeout)?);
    let pg_outbox = Arc::new(PgOutbox::connect(database).await?);
    let telemetry = telemetry::start(SERVICE_NAME)?;
    Ok(RelayModule::builder()
        .with_component_parameters::<PostgresRelayOutbox>(PostgresRelayOutboxParameters {
            outbox: pg_outbox,
        })
        .with_component_parameters::<KafkaRelayPublisher>(KafkaRelayPublisherParameters {
            publisher,
        })
        .with_component_parameters::<OtlpTelemetry>(OtlpTelemetryParameters {
            telemetry: Mutex::new(Some(telemetry)),
        })
        .with_component_parameters::<OutboxSettings>(OutboxSettingsParameters { config: outbox })
        .build())
}

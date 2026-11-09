//! Загрузка конфигурации: файл `CONFIG_FILE` (заголовок, строгий `spec`),
//! секреты из env и проверка значений.
//!
//! Файл читает crate `config` (формат YAML), `spec` декодирует serde с
//! `deny_unknown_fields`; `serde_path_to_error` добавляет к ошибке путь
//! ключа (`spec.shutdown.drainTimout`). Источников env, кроме секретов и
//! `POD_NAME`, нет.

use std::path::{Path, PathBuf};

use config::{Config as ConfigLoader, ConfigError, File, FileFormat};
use serde::Deserialize;
use serde::de::DeserializeOwned;

use super::database;
use super::kafka::{KafkaConfig, SchemaRegistryConfig};
use super::telemetry::{
    LoggingSpec, MetricsSpec, POD_NAME_ENV, TelemetrySections, TelemetrySpec, TracingSpec,
};
use super::{
    API_VERSION, AppConfig, CONFIG_FILE_ENV, DATABASE_URI_ENV, DeliveryEstimateConfig,
    IdentityConfig, KIND, ManagementConfig, ReconcilerConfig, SERVER_ROLE,
    SPICEDB_PRESHARED_KEY_ENV, ServerConfig, ShutdownConfig, SimulatorConfig, SpiceDbConfig,
    TemporalConfig, TrackingConfig, VALKEY_URL_ENV, ValkeyConfig, WORKER_ROLE, WorkerConfig,
};

/// Заголовок файла: проверяется до `spec`, чтобы файл чужой версии или kind
/// не давал ошибок схемы.
#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
struct Header {
    api_version: String,
    kind: String,
    metadata: Metadata,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Metadata {
    /// Роль процесса.
    name: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields, rename_all = "camelCase")]
struct Document {
    #[serde(rename = "apiVersion")]
    _api_version: String,
    #[serde(rename = "kind")]
    _kind: String,
    #[serde(rename = "metadata")]
    _metadata: Metadata,
    spec: Spec,
}

/// `spec` kind `Delivery`: секции всех ролей. Секции со значениями по
/// умолчанию необязательны; секции без них обязательны только процессу,
/// который их читает.
#[derive(Deserialize)]
#[serde(deny_unknown_fields, rename_all = "camelCase")]
struct Spec {
    #[serde(default)]
    server: ServerConfig,
    #[serde(default)]
    management: ManagementConfig,
    #[serde(default)]
    shutdown: ShutdownConfig,
    #[serde(default)]
    identity: Option<IdentityConfig>,
    #[serde(default)]
    spicedb: Option<SpiceDbConfig>,
    #[serde(default)]
    reconciler: ReconcilerConfig,
    #[serde(default)]
    temporal: Option<TemporalConfig>,
    #[serde(default)]
    database: database::Config,
    #[serde(default)]
    simulator: Option<SimulatorConfig>,
    #[serde(default)]
    tracking: Option<TrackingConfig>,
    #[serde(default)]
    kafka: Option<KafkaConfig>,
    #[serde(default)]
    schema_registry: Option<SchemaRegistryConfig>,
    #[serde(default)]
    delivery_estimate: Option<DeliveryEstimateConfig>,
    #[serde(default)]
    telemetry: TelemetrySpec,
    #[serde(default)]
    tracing: TracingSpec,
    #[serde(default)]
    metrics: MetricsSpec,
    #[serde(default)]
    logging: LoggingSpec,
}

impl Spec {
    fn telemetry(&mut self) -> TelemetrySections {
        TelemetrySections {
            telemetry: std::mem::take(&mut self.telemetry),
            tracing: std::mem::take(&mut self.tracing),
            metrics: std::mem::take(&mut self.metrics),
            logging: std::mem::take(&mut self.logging),
        }
    }
}

/// Конфигурация процесса `delivery` из `CONFIG_FILE` и env.
pub fn load() -> Result<AppConfig, ConfigError> {
    load_from(config_file().as_deref(), |name| std::env::var(name).ok())
}

/// Конфигурация `delivery-worker` из `CONFIG_FILE` и env; секции процесса
/// `delivery` в файле допустимы.
pub fn load_worker() -> Result<WorkerConfig, ConfigError> {
    load_worker_from(config_file().as_deref(), |name| std::env::var(name).ok())
}

/// Секции Kafka и Schema Registry для `delivery-admin` из файла pod, в
/// котором запущена команда: сверяются только `apiVersion` и `kind`.
pub fn load_kafka() -> Result<KafkaConfig, ConfigError> {
    load_kafka_from(config_file().as_deref())
}

fn config_file() -> Option<PathBuf> {
    std::env::var_os(CONFIG_FILE_ENV).map(PathBuf::from)
}

pub(super) fn load_from(
    config_file: Option<&Path>,
    env: impl Fn(&str) -> Option<String>,
) -> Result<AppConfig, ConfigError> {
    let mut spec = read(config_file, Some(SERVER_ROLE))?;
    let telemetry = spec.telemetry().resolve(SERVER_ROLE, env(POD_NAME_ENV))?;
    let mut spicedb = required(spec.spicedb, "spec.spicedb")?;
    spicedb.preshared_key = secret(&env, SPICEDB_PRESHARED_KEY_ENV)?;
    let mut database = spec.database;
    database.uri = secret(&env, DATABASE_URI_ENV)?;
    let config = AppConfig {
        server: spec.server,
        management: spec.management,
        shutdown: spec.shutdown,
        identity: required(spec.identity, "spec.identity")?,
        spicedb,
        reconciler: spec.reconciler,
        temporal: required(spec.temporal, "spec.temporal")?,
        database,
        simulator: required(spec.simulator, "spec.simulator")?,
        tracking: required(spec.tracking, "spec.tracking")?,
        valkey: ValkeyConfig {
            url: secret(&env, VALKEY_URL_ENV)?,
        },
        kafka: kafka(spec.kafka, spec.schema_registry)?,
        delivery_estimate: required(spec.delivery_estimate, "spec.deliveryEstimate")?,
        telemetry,
    };
    config.server.validate()?;
    config.management.validate(&config.server)?;
    config.shutdown.validate()?;
    config.spicedb.validate()?;
    config.reconciler.validate()?;
    config.temporal.validate()?;
    config.database.validate()?;
    config.simulator.validate()?;
    config.tracking.validate()?;
    config.valkey.validate()?;
    config.kafka.validate()?;

    Ok(config)
}

pub(super) fn load_worker_from(
    config_file: Option<&Path>,
    env: impl Fn(&str) -> Option<String>,
) -> Result<WorkerConfig, ConfigError> {
    let mut spec = read(config_file, Some(WORKER_ROLE))?;
    let telemetry = spec.telemetry().resolve(WORKER_ROLE, env(POD_NAME_ENV))?;
    let mut database = spec.database;
    database.uri = secret(&env, DATABASE_URI_ENV)?;
    let config = WorkerConfig {
        management: spec.management,
        shutdown: spec.shutdown,
        database,
        temporal: required(spec.temporal, "spec.temporal")?,
        telemetry,
    };
    config.management.validate_port()?;
    config.shutdown.validate()?;
    config.database.validate()?;
    config.temporal.validate()?;

    Ok(config)
}

pub(super) fn load_kafka_from(config_file: Option<&Path>) -> Result<KafkaConfig, ConfigError> {
    let spec = read(config_file, None)?;
    let config = kafka(spec.kafka, spec.schema_registry)?;
    config.validate()?;

    Ok(config)
}

/// Читает файл и проверяет заголовок; `role` — ожидаемое `metadata.name`
/// (`None` — операторская команда, которая принимает файл любой роли).
fn read(config_file: Option<&Path>, role: Option<&str>) -> Result<Spec, ConfigError> {
    let Some(path) = config_file else {
        return Err(ConfigError::Message(format!(
            "{CONFIG_FILE_ENV} is required"
        )));
    };
    let loaded = ConfigLoader::builder()
        .add_source(File::from(path).format(FileFormat::Yaml).required(true))
        .build()?;
    let header: Header = decode(loaded.clone())?;
    if header.api_version != API_VERSION || header.kind != KIND {
        return Err(ConfigError::Message(format!(
            "unsupported config apiVersion {:?} kind {:?}: expected apiVersion {API_VERSION} \
             and kind {KIND}",
            header.api_version, header.kind
        )));
    }
    if let Some(role) = role
        && header.metadata.name != role
    {
        return Err(ConfigError::Message(format!(
            "config metadata.name {:?} does not match the process role {role}",
            header.metadata.name
        )));
    }
    let document: Document = decode(loaded)?;
    Ok(document.spec)
}

/// Строгое декодирование с путём ключа в ошибке.
fn decode<T: DeserializeOwned>(loaded: ConfigLoader) -> Result<T, ConfigError> {
    serde_path_to_error::deserialize(loaded)
        .map_err(|error| ConfigError::Message(format!("{}: {}", error.path(), error.inner())))
}

fn required<T>(section: Option<T>, key: &str) -> Result<T, ConfigError> {
    section.ok_or_else(|| ConfigError::Message(format!("{key} is required")))
}

/// Обязательный секрет из env: отсутствие и пустое значение — ошибка.
/// Значение не попадает в сообщение.
fn secret(env: &impl Fn(&str) -> Option<String>, name: &str) -> Result<String, ConfigError> {
    env(name)
        .filter(|value| !value.trim().is_empty())
        .ok_or_else(|| ConfigError::Message(format!("{name} is required")))
}

fn kafka(
    kafka: Option<KafkaConfig>,
    schema_registry: Option<SchemaRegistryConfig>,
) -> Result<KafkaConfig, ConfigError> {
    let mut kafka = required(kafka, "spec.kafka")?;
    kafka.schema_registry_url = required(schema_registry, "spec.schemaRegistry")?.url;
    Ok(kafka)
}

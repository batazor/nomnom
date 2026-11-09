//! Конфигурация процессов `delivery`, `delivery-worker` и команды
//! `delivery-admin` (ADR 17): версионированный YAML-файл `CONFIG_FILE` вида
//! ресурса Kubernetes (`apiVersion: config.nomnom.dev/v1alpha1`,
//! `kind: Delivery`, `metadata.name` — роль процесса, `spec`) и секреты из
//! env под их именами (`DATABASE__URI`, `VALKEY__URL`,
//! `SPICEDB__PRESHARED_KEY`). Переменные `SECTION__KEY` несекретных настроек
//! не читаются: у настройки один источник.
//!
//! Ключи `spec` — camelCase имён прежних переменных
//! (`TEMPORAL__HOST_PORT` → `spec.temporal.hostPort`), длительности — строки
//! (`500ms`, `5s`, `2m`). Неизвестный ключ на любом уровне — ошибка с путём
//! ключа; полей для секретов в `spec` нет, поэтому секрет в файле — тоже
//! неизвестный ключ. Процесс проверяет весь `spec` kind `Delivery`, но
//! требует только свои секции: секции другой роли допустимы.

pub mod database;
mod estimate;
mod infrastructure;
mod kafka;
mod load;
mod simulator;
mod telemetry;
mod temporal;
mod tracking;

use std::time::Duration;

use config::ConfigError;
use serde::Deserialize;

pub use estimate::{
    DELIVERY_ESTIMATE_BASELINE_COURIER_LEAD_KEY, DELIVERY_ESTIMATE_BASELINE_TRANSIT_KEY,
    DELIVERY_ESTIMATE_SAMPLE_SIZE_KEY, DeliveryEstimateConfig,
};
pub use infrastructure::{ReconcilerConfig, SpiceDbConfig};
pub use kafka::{
    InitialOffset, KAFKA_BROKERS_KEY, KAFKA_CONSUMER_GROUP_KEY, KafkaConfig,
    SCHEMA_REGISTRY_URL_KEY,
};
pub use load::{load, load_kafka, load_worker};
#[cfg(test)]
use load::{load_from, load_kafka_from, load_worker_from};
pub use simulator::{
    SIMULATOR_INTERVAL_KEY, SIMULATOR_MAX_DELAY_KEY, SIMULATOR_MIN_DELAY_KEY,
    SIMULATOR_TRACK_INTERVAL_KEY, SimulatorConfig,
};
pub use telemetry::{
    DEFAULT_METRICS_EXPORT_INTERVAL, LogLevel, OtlpProtocol, POD_NAME_ENV, TelemetryConfig,
};
pub use temporal::{
    TEMPORAL_CONNECT_TIMEOUT_KEY, TEMPORAL_HOST_PORT_KEY, TEMPORAL_NAMESPACE_KEY, TemporalConfig,
};
pub use tracking::{TRACKING_PERSIST_INTERVAL_KEY, TrackingConfig, VALKEY_URL_ENV, ValkeyConfig};

/// Переменная окружения с путём к файлу конфигурации.
pub const CONFIG_FILE_ENV: &str = "CONFIG_FILE";
/// Версия формата файла, которую знает процесс.
pub const API_VERSION: &str = "config.nomnom.dev/v1alpha1";
/// Kind файла: все роли сервиса делят его.
pub const KIND: &str = "Delivery";
/// Роль процесса gRPC API (`metadata.name`) и его `service.name` по умолчанию.
pub const SERVER_ROLE: &str = "delivery";
/// Роль Temporal worker (`metadata.name`) и его `service.name` по умолчанию.
pub const WORKER_ROLE: &str = "delivery-worker";

pub const DEFAULT_PORT: u16 = 8083;
pub const DEFAULT_MANAGEMENT_PORT: u16 = 9083;
pub const DEFAULT_DRAIN_TIMEOUT: Duration = Duration::from_secs(20);
pub const DEFAULT_READINESS_DELAY: Duration = Duration::from_secs(5);

/// Ключи service identity (ADR 27) и user assertions BFF (ADR 32).
pub const IDENTITY_KEY: &str = "spec.identity";

/// SpiceDB (ADR 18): endpoint в файле, preshared key — секрет из env.
pub const SPICEDB_ENDPOINT_KEY: &str = "spec.spicedb.endpoint";
pub const SPICEDB_PRESHARED_KEY_ENV: &str = "SPICEDB__PRESHARED_KEY";
pub const DEFAULT_SPICEDB_TIMEOUT: Duration = Duration::from_secs(2);

/// Reconciliation of relationships (FR-445): значения по умолчанию.
pub const DEFAULT_RECONCILER_INTERVAL: Duration = Duration::from_secs(5 * 60);
pub const DEFAULT_RECONCILER_GRACE: Duration = Duration::from_secs(10 * 60);
pub const DEFAULT_RECONCILER_PAGE_SIZE: u32 = 500;
/// Максимум `ReadRelationships` limit SpiceDB по умолчанию.
pub const MAX_RECONCILER_PAGE_SIZE: u32 = 1000;

/// PostgreSQL (ADR 8, ADR 31, ADR 0001 контекста): URI — секрет из env,
/// лимиты пула — `spec.database`.
pub const DATABASE_URI_ENV: &str = "DATABASE__URI";
pub const DEFAULT_DATABASE_MAX_CONNS: u32 = 10;

#[derive(Debug, Deserialize, PartialEq, Eq)]
#[serde(deny_unknown_fields, rename_all = "camelCase", default)]
pub struct ServerConfig {
    pub port: u16,
}

impl Default for ServerConfig {
    fn default() -> Self {
        Self { port: DEFAULT_PORT }
    }
}

/// Технический порт (Kubernetes-имя `metrics`): health и metrics отделены
/// от бизнес-порта.
#[derive(Debug, Deserialize, PartialEq, Eq)]
#[serde(deny_unknown_fields, rename_all = "camelCase", default)]
pub struct ManagementConfig {
    pub port: u16,
}

impl Default for ManagementConfig {
    fn default() -> Self {
        Self {
            port: DEFAULT_MANAGEMENT_PORT,
        }
    }
}

#[derive(Debug, Deserialize, PartialEq, Eq)]
#[serde(deny_unknown_fields, rename_all = "camelCase", default)]
pub struct ShutdownConfig {
    /// Ожидание распространения not ready до остановки приёма работы.
    /// Нулевое значение допустимо для процессов без входящего трафика.
    #[serde(with = "humantime_serde")]
    pub readiness_delay: Duration,
    /// Deadline на завершение начатой работы после `SIGTERM`/`SIGINT`.
    #[serde(with = "humantime_serde")]
    pub drain_timeout: Duration,
}

impl Default for ShutdownConfig {
    fn default() -> Self {
        Self {
            readiness_delay: DEFAULT_READINESS_DELAY,
            drain_timeout: DEFAULT_DRAIN_TIMEOUT,
        }
    }
}

/// Проверка projected ServiceAccount token (ADR 27) и user assertions BFF
/// (ADR 32), секция `spec.identity`. Все поля обязательны для процесса с
/// gRPC API: отсутствие ключа — ошибка загрузки, пустое значение — ошибка
/// startup.
#[derive(Debug, Default, Deserialize, PartialEq, Eq)]
#[serde(deny_unknown_fields, rename_all = "camelCase")]
pub struct IdentityConfig {
    /// Ожидаемый `iss` token (issuer кластера).
    pub issuer: String,
    /// Audience контекста, которую должен содержать `aud`.
    pub audience: String,
    /// Путь к JWKS кластера (`/openid/v1/jwks`).
    pub jwks_file: String,
    /// Allowlist `namespace/serviceaccount=service-name` через запятую.
    pub service_accounts: String,
    /// Ожидаемый `iss` user assertion (`nomnom-bff`).
    pub user_issuer: String,
    /// Путь к JWKS BFF (ключи EC P-256 для ES256).
    pub user_jwks_file: String,
    /// Services, которым разрешено передавать user assertion, через запятую.
    pub user_delegates: String,
}

/// Конфигурация процесса `delivery`.
#[derive(Debug, PartialEq)]
pub struct AppConfig {
    pub server: ServerConfig,
    pub management: ManagementConfig,
    pub shutdown: ShutdownConfig,
    pub identity: IdentityConfig,
    pub spicedb: SpiceDbConfig,
    pub reconciler: ReconcilerConfig,
    pub temporal: TemporalConfig,
    pub database: database::Config,
    pub simulator: SimulatorConfig,
    pub tracking: TrackingConfig,
    pub valkey: ValkeyConfig,
    pub kafka: KafkaConfig,
    pub delivery_estimate: DeliveryEstimateConfig,
    pub telemetry: TelemetryConfig,
}

/// Конфигурация `delivery-worker` (FR-708): технический порт, остановка,
/// PostgreSQL, Temporal и телеметрия. gRPC API, SpiceDB, Valkey и Kafka
/// процессу не нужны.
#[derive(Debug, PartialEq)]
pub struct WorkerConfig {
    pub management: ManagementConfig,
    pub shutdown: ShutdownConfig,
    pub database: database::Config,
    pub temporal: TemporalConfig,
    pub telemetry: TelemetryConfig,
}

impl ShutdownConfig {
    fn validate(&self) -> Result<(), ConfigError> {
        if self.drain_timeout.is_zero() {
            return Err(ConfigError::Message(
                "spec.shutdown.drainTimeout must be positive".to_owned(),
            ));
        }
        Ok(())
    }
}

impl ManagementConfig {
    fn validate_port(&self) -> Result<(), ConfigError> {
        if self.port == 0 {
            return Err(ConfigError::Message(
                "spec.management.port must be between 1 and 65535".to_owned(),
            ));
        }
        Ok(())
    }

    fn validate(&self, server: &ServerConfig) -> Result<(), ConfigError> {
        self.validate_port()?;
        if self.port == server.port {
            return Err(ConfigError::Message(format!(
                "spec.management.port must differ from spec.server.port {}",
                server.port
            )));
        }
        Ok(())
    }
}

impl ServerConfig {
    fn validate(&self) -> Result<(), ConfigError> {
        if self.port == 0 {
            return Err(ConfigError::Message(
                "spec.server.port must be between 1 and 65535".to_owned(),
            ));
        }
        Ok(())
    }
}

#[cfg(test)]
mod tests;

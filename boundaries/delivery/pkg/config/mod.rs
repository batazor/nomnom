pub mod database;
mod estimate;
mod infrastructure;
mod kafka;
mod load;
mod simulator;
mod temporal;
mod tracking;

use std::time::Duration;

use config::ConfigError;
use serde::Deserialize;

pub use estimate::{
    DELIVERY_ESTIMATE_BASELINE_COURIER_LEAD_ENV, DELIVERY_ESTIMATE_BASELINE_TRANSIT_ENV,
    DELIVERY_ESTIMATE_SAMPLE_SIZE_ENV, DeliveryEstimateConfig,
};
pub use infrastructure::{ReconcilerConfig, SpiceDbConfig};
pub use kafka::{
    InitialOffset, KAFKA_BROKERS_ENV, KAFKA_CONSUMER_GROUP_ENV, KAFKA_INITIAL_OFFSET_ENV,
    KafkaConfig,
};
#[cfg(test)]
use load::{
    configure_delivery_estimate, configure_identity, configure_kafka, configure_reconciler,
    configure_settings, configure_simulator, configure_spicedb, configure_temporal,
    configure_tracking, load_from, load_worker_from,
};
pub use load::{load, load_worker};
pub use simulator::{
    SIMULATOR_INTERVAL_ENV, SIMULATOR_MAX_DELAY_ENV, SIMULATOR_MIN_DELAY_ENV,
    SIMULATOR_TRACK_INTERVAL_ENV, SimulatorConfig,
};
pub use temporal::{
    TEMPORAL_CONNECT_TIMEOUT_ENV, TEMPORAL_HOST_PORT_ENV, TEMPORAL_NAMESPACE_ENV, TemporalConfig,
};
pub use tracking::{
    TRACKING_BLOCK_METERS_ENV, TRACKING_CITY_BLOCKS_ENV, TRACKING_CITY_ORIGIN_LATITUDE_ENV,
    TRACKING_CITY_ORIGIN_LONGITUDE_ENV, TRACKING_PERSIST_INTERVAL_ENV, TrackingConfig,
    VALKEY_URL_ENV, ValkeyConfig,
};

const CONFIG_KEY_PORT: &str = "server.port";
pub const DEFAULT_PORT: u16 = 8083;
pub const PORT_ENV: &str = "SERVER__PORT";

const CONFIG_KEY_MANAGEMENT_PORT: &str = "management.port";
pub const DEFAULT_MANAGEMENT_PORT: u16 = 9083;
pub const MANAGEMENT_PORT_ENV: &str = "MANAGEMENT__PORT";

const CONFIG_KEY_DRAIN_TIMEOUT: &str = "shutdown.drain_timeout";
pub const DEFAULT_DRAIN_TIMEOUT: &str = "20s";
pub const DRAIN_TIMEOUT_ENV: &str = "SHUTDOWN__DRAIN_TIMEOUT";

const CONFIG_KEY_READINESS_DELAY: &str = "shutdown.readiness_delay";
pub const DEFAULT_READINESS_DELAY: &str = "5s";
pub const READINESS_DELAY_ENV: &str = "SHUTDOWN__READINESS_DELAY";

/// Service identity (ADR 27) и user assertions BFF (ADR 32): ключ
/// конфигурации и переменная окружения.
const IDENTITY_SETTINGS: [(&str, &str); 7] = [
    ("identity.issuer", ISSUER_ENV),
    ("identity.audience", AUDIENCE_ENV),
    ("identity.jwks_file", JWKS_FILE_ENV),
    ("identity.service_accounts", SERVICE_ACCOUNTS_ENV),
    ("identity.user_issuer", USER_ISSUER_ENV),
    ("identity.user_jwks_file", USER_JWKS_FILE_ENV),
    ("identity.user_delegates", USER_DELEGATES_ENV),
];
pub const ISSUER_ENV: &str = "IDENTITY__ISSUER";
pub const AUDIENCE_ENV: &str = "IDENTITY__AUDIENCE";
pub const JWKS_FILE_ENV: &str = "IDENTITY__JWKS_FILE";
pub const SERVICE_ACCOUNTS_ENV: &str = "IDENTITY__SERVICE_ACCOUNTS";
pub const USER_ISSUER_ENV: &str = "IDENTITY__USER_ISSUER";
pub const USER_JWKS_FILE_ENV: &str = "IDENTITY__USER_JWKS_FILE";
pub const USER_DELEGATES_ENV: &str = "IDENTITY__USER_DELEGATES";

/// SpiceDB (ADR 18): ключ конфигурации, переменная окружения и значение по умолчанию.
const SPICEDB_SETTINGS: [(&str, &str, &str); 4] = [
    ("spicedb.endpoint", SPICEDB_ENDPOINT_ENV, ""),
    ("spicedb.preshared_key", SPICEDB_PRESHARED_KEY_ENV, ""),
    ("spicedb.insecure", SPICEDB_INSECURE_ENV, "false"),
    (
        "spicedb.timeout",
        SPICEDB_TIMEOUT_ENV,
        DEFAULT_SPICEDB_TIMEOUT,
    ),
];
pub const SPICEDB_ENDPOINT_ENV: &str = "SPICEDB__ENDPOINT";
pub const SPICEDB_PRESHARED_KEY_ENV: &str = "SPICEDB__PRESHARED_KEY";
pub const SPICEDB_INSECURE_ENV: &str = "SPICEDB__INSECURE";
pub const SPICEDB_TIMEOUT_ENV: &str = "SPICEDB__TIMEOUT";
pub const DEFAULT_SPICEDB_TIMEOUT: &str = "2s";

/// Reconciliation of relationships (FR-445): ключ, переменная, значение по умолчанию.
const RECONCILER_SETTINGS: [(&str, &str, &str); 3] = [
    ("reconciler.interval", RECONCILER_INTERVAL_ENV, "5m"),
    ("reconciler.grace", RECONCILER_GRACE_ENV, "10m"),
    ("reconciler.page_size", RECONCILER_PAGE_SIZE_ENV, "500"),
];
pub const RECONCILER_INTERVAL_ENV: &str = "RECONCILER__INTERVAL";
pub const RECONCILER_GRACE_ENV: &str = "RECONCILER__GRACE";
pub const RECONCILER_PAGE_SIZE_ENV: &str = "RECONCILER__PAGE_SIZE";
/// Максимум `ReadRelationships` limit SpiceDB по умолчанию.
pub const MAX_RECONCILER_PAGE_SIZE: u32 = 1000;

/// PostgreSQL (ADR 8, ADR 31, ADR 0001 контекста): ключ, переменная, значение по умолчанию.
const DATABASE_SETTINGS: [(&str, &str, &str); 6] = [
    ("database.uri", DATABASE_URI_ENV, ""),
    (
        "database.max_conns",
        DATABASE_MAX_CONNS_ENV,
        DEFAULT_DATABASE_MAX_CONNS,
    ),
    ("database.min_conns", DATABASE_MIN_CONNS_ENV, "0"),
    ("database.max_lifetime", DATABASE_MAX_LIFETIME_ENV, "30m"),
    ("database.idle_timeout", DATABASE_IDLE_TIMEOUT_ENV, "5m"),
    (
        "database.acquire_timeout",
        DATABASE_ACQUIRE_TIMEOUT_ENV,
        "5s",
    ),
];
pub const DATABASE_URI_ENV: &str = "DATABASE__URI";
pub const DATABASE_MAX_CONNS_ENV: &str = "DATABASE__MAX_CONNS";
pub const DATABASE_MIN_CONNS_ENV: &str = "DATABASE__MIN_CONNS";
pub const DATABASE_MAX_LIFETIME_ENV: &str = "DATABASE__MAX_LIFETIME";
pub const DATABASE_IDLE_TIMEOUT_ENV: &str = "DATABASE__IDLE_TIMEOUT";
pub const DATABASE_ACQUIRE_TIMEOUT_ENV: &str = "DATABASE__ACQUIRE_TIMEOUT";
pub const DEFAULT_DATABASE_MAX_CONNS: &str = "10";

#[derive(Debug, Deserialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct ServerConfig {
    pub port: u16,
}

/// Технический порт (Kubernetes-имя `metrics`): health и metrics отделены
/// от бизнес-порта.
#[derive(Debug, Deserialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct ManagementConfig {
    pub port: u16,
}

#[derive(Debug, Deserialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct ShutdownConfig {
    /// Ожидание распространения not ready до остановки приёма работы.
    /// Нулевое значение допустимо для процессов без входящего трафика.
    #[serde(with = "humantime_serde")]
    pub readiness_delay: Duration,
    /// Deadline на завершение начатой работы после `SIGTERM`/`SIGINT`.
    #[serde(with = "humantime_serde")]
    pub drain_timeout: Duration,
}

/// Проверка projected ServiceAccount token (ADR 27) и user assertions BFF
/// (ADR 32). Все поля обязательны для процесса с gRPC API: пустое значение —
/// ошибка startup.
#[derive(Debug, Default, Deserialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
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

#[derive(Debug, Deserialize, PartialEq)]
#[serde(deny_unknown_fields)]
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
}

/// Конфигурация `delivery-worker` (FR-708): технический порт, остановка,
/// PostgreSQL и Temporal. gRPC API, SpiceDB, Valkey и Kafka процессу не нужны.
#[derive(Debug, Deserialize, PartialEq)]
#[serde(deny_unknown_fields)]
pub struct WorkerConfig {
    pub management: ManagementConfig,
    pub shutdown: ShutdownConfig,
    pub database: database::Config,
    pub temporal: TemporalConfig,
}

impl ShutdownConfig {
    fn validate(&self) -> Result<(), ConfigError> {
        if self.drain_timeout.is_zero() {
            return Err(ConfigError::Message(
                "shutdown drain timeout must be positive".to_owned(),
            ));
        }
        Ok(())
    }
}

impl ManagementConfig {
    fn validate_port(&self) -> Result<(), ConfigError> {
        if self.port == 0 {
            return Err(ConfigError::Message(
                "management port must be between 1 and 65535".to_owned(),
            ));
        }
        Ok(())
    }

    fn validate(&self, server: &ServerConfig) -> Result<(), ConfigError> {
        self.validate_port()?;
        if self.port == server.port {
            return Err(ConfigError::Message(format!(
                "management port must differ from server port {}",
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
                "server port must be between 1 and 65535".to_owned(),
            ));
        }
        Ok(())
    }
}

#[cfg(test)]
mod tests;

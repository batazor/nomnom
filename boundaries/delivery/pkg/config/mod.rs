mod infrastructure;
mod load;

use std::time::Duration;

use config::ConfigError;
use serde::Deserialize;

pub use infrastructure::{
    DatabaseConfig, KafkaConfig, OutboxConfig, ReconcilerConfig, SpiceDbConfig,
};
pub use load::load;
#[cfg(test)]
use load::{
    configure_identity, configure_reconciler, configure_settings, configure_spicedb, load_from,
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
const DATABASE_SETTINGS: [(&str, &str, &str); 2] = [
    ("database.url", DATABASE_URL_ENV, ""),
    (
        "database.max_conns",
        DATABASE_MAX_CONNS_ENV,
        DEFAULT_DATABASE_MAX_CONNS,
    ),
];
pub const DATABASE_URL_ENV: &str = "DATABASE__URL";
pub const DATABASE_MAX_CONNS_ENV: &str = "DATABASE__MAX_CONNS";
pub const DEFAULT_DATABASE_MAX_CONNS: &str = "10";

/// Kafka (ADR 31): ключ, переменная, значение по умолчанию. Brokers обязательны
/// только для `delivery-relay`; основной процесс к Kafka не подключается.
const KAFKA_SETTINGS: [(&str, &str, &str); 2] = [
    ("kafka.brokers", KAFKA_BROKERS_ENV, ""),
    (
        "kafka.client_id",
        KAFKA_CLIENT_ID_ENV,
        DEFAULT_KAFKA_CLIENT_ID,
    ),
];
pub const KAFKA_BROKERS_ENV: &str = "KAFKA__BROKERS";
pub const KAFKA_CLIENT_ID_ENV: &str = "KAFKA__CLIENT_ID";
pub const DEFAULT_KAFKA_CLIENT_ID: &str = "delivery-relay";

/// Outbox relay (ADR 13, ADR 31): ключ, переменная, значение по умолчанию.
const OUTBOX_SETTINGS: [(&str, &str, &str); 5] = [
    ("outbox.poll_interval", OUTBOX_POLL_INTERVAL_ENV, "1s"),
    ("outbox.batch_size", OUTBOX_BATCH_SIZE_ENV, "100"),
    ("outbox.publish_timeout", OUTBOX_PUBLISH_TIMEOUT_ENV, "10s"),
    ("outbox.retention", OUTBOX_RETENTION_ENV, "7d"),
    ("outbox.cleanup_interval", OUTBOX_CLEANUP_INTERVAL_ENV, "1h"),
];
pub const OUTBOX_POLL_INTERVAL_ENV: &str = "OUTBOX__POLL_INTERVAL";
pub const OUTBOX_BATCH_SIZE_ENV: &str = "OUTBOX__BATCH_SIZE";
pub const OUTBOX_PUBLISH_TIMEOUT_ENV: &str = "OUTBOX__PUBLISH_TIMEOUT";
pub const OUTBOX_RETENTION_ENV: &str = "OUTBOX__RETENTION";
pub const OUTBOX_CLEANUP_INTERVAL_ENV: &str = "OUTBOX__CLEANUP_INTERVAL";

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
/// (ADR 32). Пустые issuer, audience или jwks_file означают, что проверка
/// выключена и вызовы отклоняются. Пустые user_issuer, user_jwks_file или
/// user_delegates выключают только user assertions: вызов с assertion
/// отклоняется, service-вызовы работают как раньше.
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

#[derive(Debug, Deserialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct AppConfig {
    pub server: ServerConfig,
    pub management: ManagementConfig,
    pub shutdown: ShutdownConfig,
    pub identity: IdentityConfig,
    pub spicedb: SpiceDbConfig,
    pub reconciler: ReconcilerConfig,
    pub database: DatabaseConfig,
    pub kafka: KafkaConfig,
    pub outbox: OutboxConfig,
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
    fn validate(&self, server: &ServerConfig) -> Result<(), ConfigError> {
        if self.port == 0 {
            return Err(ConfigError::Message(
                "management port must be between 1 and 65535".to_owned(),
            ));
        }
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

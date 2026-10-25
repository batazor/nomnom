use std::path::Path;
use std::time::Duration;

use config::{Config as ConfigLoader, ConfigError, File};
use config::{ConfigBuilder, builder::DefaultState};
use serde::Deserialize;

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

/// Kafka-клиент `delivery-relay`.
#[derive(Clone, Debug, Default, Deserialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct KafkaConfig {
    /// `host:port` через запятую (`bootstrap.servers`).
    pub brokers: String,
    /// `client.id` producer'а.
    pub client_id: String,
}

impl KafkaConfig {
    /// Brokers обязательны для relay: пустое значение — ошибка конфигурации.
    pub fn require(&self) -> Result<(), ConfigError> {
        if self.brokers.trim().is_empty() {
            return Err(ConfigError::Message(format!(
                "{KAFKA_BROKERS_ENV} is required"
            )));
        }
        if self.client_id.trim().is_empty() {
            return Err(ConfigError::Message(format!(
                "{KAFKA_CLIENT_ID_ENV} must not be empty"
            )));
        }
        Ok(())
    }
}

/// Polling relay Outbox (ADR 31).
#[derive(Clone, Debug, Deserialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct OutboxConfig {
    /// Пауза между циклами опроса.
    #[serde(with = "humantime_serde")]
    pub poll_interval: Duration,
    /// Максимум записей одной пачки.
    pub batch_size: u32,
    /// Deadline подтверждения брокера одной записи (`message.timeout.ms`).
    #[serde(with = "humantime_serde")]
    pub publish_timeout: Duration,
    /// Опубликованные записи старше retention удаляются.
    #[serde(with = "humantime_serde")]
    pub retention: Duration,
    /// Интервал очистки по retention.
    #[serde(with = "humantime_serde")]
    pub cleanup_interval: Duration,
}

impl Default for OutboxConfig {
    fn default() -> Self {
        Self {
            poll_interval: Duration::from_secs(1),
            batch_size: 100,
            publish_timeout: Duration::from_secs(10),
            retention: Duration::from_secs(7 * 24 * 60 * 60),
            cleanup_interval: Duration::from_secs(60 * 60),
        }
    }
}

impl OutboxConfig {
    fn validate(&self) -> Result<(), ConfigError> {
        let positive = [
            ("poll_interval", self.poll_interval),
            ("publish_timeout", self.publish_timeout),
            ("retention", self.retention),
            ("cleanup_interval", self.cleanup_interval),
        ];
        if let Some((name, _)) = positive.iter().find(|(_, value)| value.is_zero()) {
            return Err(ConfigError::Message(format!(
                "outbox {name} must be positive"
            )));
        }
        if self.batch_size == 0 {
            return Err(ConfigError::Message(
                "outbox batch_size must be positive".to_owned(),
            ));
        }
        Ok(())
    }
}

/// Хранилище `delivery`. URL обязателен для процесса и миграций: пустое
/// значение завершает startup (см. `di::postgres`). URL приходит из Secret.
#[derive(Clone, Deserialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct DatabaseConfig {
    /// libpq connection string или URL `postgres://user:password@host/db`.
    pub url: String,
    /// Максимум соединений пула процесса.
    pub max_conns: u32,
}

impl Default for DatabaseConfig {
    fn default() -> Self {
        Self {
            url: String::new(),
            max_conns: 10,
        }
    }
}

/// URL содержит пароль и не попадает в логи.
impl std::fmt::Debug for DatabaseConfig {
    fn fmt(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        formatter
            .debug_struct("DatabaseConfig")
            .field(
                "url",
                &if self.url.is_empty() {
                    ""
                } else {
                    "<redacted>"
                },
            )
            .field("max_conns", &self.max_conns)
            .finish()
    }
}

impl DatabaseConfig {
    /// PostgreSQL задан, если задан URL.
    #[must_use]
    pub fn enabled(&self) -> bool {
        !self.url.trim().is_empty()
    }

    fn validate(&self) -> Result<(), ConfigError> {
        if self.max_conns == 0 {
            return Err(ConfigError::Message(
                "database max_conns must be positive".to_owned(),
            ));
        }
        Ok(())
    }
}

/// Фоновая сверка relationships `courier#self` и `delivery#courier` с
/// business state процесса.
#[derive(Debug, Deserialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct ReconcilerConfig {
    /// Интервал между проходами.
    #[serde(with = "humantime_serde")]
    pub interval: Duration,
    /// Минимальный интервал между двумя проходами, на которых связь была
    /// orphan, прежде чем её удалить.
    #[serde(with = "humantime_serde")]
    pub grace: Duration,
    /// Размер страницы `ReadRelationships` и чтения хранилища.
    pub page_size: u32,
}

impl ReconcilerConfig {
    fn validate(&self) -> Result<(), ConfigError> {
        if self.interval.is_zero() {
            return Err(ConfigError::Message(
                "reconciler interval must be positive".to_owned(),
            ));
        }
        if self.page_size == 0 || self.page_size > MAX_RECONCILER_PAGE_SIZE {
            return Err(ConfigError::Message(format!(
                "reconciler page size must be between 1 and {MAX_RECONCILER_PAGE_SIZE}"
            )));
        }
        Ok(())
    }
}

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

/// Проверка прав в SpiceDB (ADR 18). Пустой `endpoint` оставляет локальную
/// авторизацию процесса.
#[derive(Clone, Deserialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct SpiceDbConfig {
    /// `host:port` или URL gRPC endpoint SpiceDB.
    pub endpoint: String,
    /// Preshared key, передаваемый как `authorization: Bearer <key>`.
    pub preshared_key: String,
    /// Plaintext без TLS — только для kind и локального запуска.
    pub insecure: bool,
    /// Deadline одного `CheckPermission`.
    #[serde(with = "humantime_serde")]
    pub timeout: Duration,
}

impl Default for SpiceDbConfig {
    fn default() -> Self {
        Self {
            endpoint: String::new(),
            preshared_key: String::new(),
            insecure: false,
            timeout: Duration::from_secs(2),
        }
    }
}

/// Ключ не попадает в логи и сообщения об ошибках.
impl std::fmt::Debug for SpiceDbConfig {
    fn fmt(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        formatter
            .debug_struct("SpiceDbConfig")
            .field("endpoint", &self.endpoint)
            .field(
                "preshared_key",
                &if self.preshared_key.is_empty() {
                    ""
                } else {
                    "<redacted>"
                },
            )
            .field("insecure", &self.insecure)
            .field("timeout", &self.timeout)
            .finish()
    }
}

impl SpiceDbConfig {
    /// SpiceDB выбран, если задан endpoint.
    #[must_use]
    pub fn enabled(&self) -> bool {
        !self.endpoint.trim().is_empty()
    }

    fn validate(&self) -> Result<(), ConfigError> {
        if !self.enabled() {
            return Ok(());
        }
        if self.preshared_key.is_empty() {
            return Err(ConfigError::Message(format!(
                "{SPICEDB_PRESHARED_KEY_ENV} is required when {SPICEDB_ENDPOINT_ENV} is set"
            )));
        }
        if self.timeout.is_zero() {
            return Err(ConfigError::Message(
                "spicedb timeout must be positive".to_owned(),
            ));
        }
        Ok(())
    }
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

pub fn load() -> Result<AppConfig, ConfigError> {
    let config_file = std::env::var("CONFIG_FILE").ok();
    load_from(config_file.as_deref().map(Path::new))
}

fn load_from(config_file: Option<&Path>) -> Result<AppConfig, ConfigError> {
    let mut builder = ConfigLoader::builder();

    if let Some(path) = config_file {
        builder = builder.add_source(File::from(path).required(true));
    }

    builder = configure_server(builder)?;
    builder = configure_management(builder)?;
    builder = configure_shutdown(builder)?;
    builder = configure_identity(builder, |name| std::env::var(name).ok())?;
    builder = configure_spicedb(builder, |name| std::env::var(name).ok())?;
    builder = configure_reconciler(builder, |name| std::env::var(name).ok())?;
    builder = configure_database(builder, |name| std::env::var(name).ok())?;
    builder = configure_settings(builder, &KAFKA_SETTINGS, |name| std::env::var(name).ok())?;
    builder = configure_settings(builder, &OUTBOX_SETTINGS, |name| std::env::var(name).ok())?;

    let config: AppConfig = builder.build()?.try_deserialize()?;
    config.server.validate()?;
    config.management.validate(&config.server)?;
    config.shutdown.validate()?;
    config.spicedb.validate()?;
    config.reconciler.validate()?;
    config.database.validate()?;
    config.outbox.validate()?;

    Ok(config)
}

fn configure_server(
    mut builder: ConfigBuilder<DefaultState>,
) -> Result<ConfigBuilder<DefaultState>, ConfigError> {
    builder = builder.set_default(CONFIG_KEY_PORT, DEFAULT_PORT)?;

    if let Ok(port) = std::env::var(PORT_ENV).or_else(|_| std::env::var("PORT")) {
        builder = builder.set_override(CONFIG_KEY_PORT, port)?;
    }

    Ok(builder)
}

fn configure_management(
    mut builder: ConfigBuilder<DefaultState>,
) -> Result<ConfigBuilder<DefaultState>, ConfigError> {
    builder = builder.set_default(CONFIG_KEY_MANAGEMENT_PORT, DEFAULT_MANAGEMENT_PORT)?;

    if let Ok(port) = std::env::var(MANAGEMENT_PORT_ENV) {
        builder = builder.set_override(CONFIG_KEY_MANAGEMENT_PORT, port)?;
    }

    Ok(builder)
}

fn configure_shutdown(
    mut builder: ConfigBuilder<DefaultState>,
) -> Result<ConfigBuilder<DefaultState>, ConfigError> {
    builder = builder.set_default(CONFIG_KEY_DRAIN_TIMEOUT, DEFAULT_DRAIN_TIMEOUT)?;

    if let Ok(timeout) = std::env::var(DRAIN_TIMEOUT_ENV) {
        builder = builder.set_override(CONFIG_KEY_DRAIN_TIMEOUT, timeout)?;
    }

    builder = builder.set_default(CONFIG_KEY_READINESS_DELAY, DEFAULT_READINESS_DELAY)?;

    if let Ok(delay) = std::env::var(READINESS_DELAY_ENV) {
        builder = builder.set_override(CONFIG_KEY_READINESS_DELAY, delay)?;
    }

    Ok(builder)
}

fn configure_identity(
    mut builder: ConfigBuilder<DefaultState>,
    env: impl Fn(&str) -> Option<String>,
) -> Result<ConfigBuilder<DefaultState>, ConfigError> {
    for (key, name) in IDENTITY_SETTINGS {
        builder = builder.set_default(key, "")?;
        if let Some(value) = env(name) {
            builder = builder.set_override(key, value)?;
        }
    }
    Ok(builder)
}

fn configure_spicedb(
    mut builder: ConfigBuilder<DefaultState>,
    env: impl Fn(&str) -> Option<String>,
) -> Result<ConfigBuilder<DefaultState>, ConfigError> {
    for (key, name, default) in SPICEDB_SETTINGS {
        builder = builder.set_default(key, default)?;
        if let Some(value) = env(name) {
            builder = builder.set_override(key, value)?;
        }
    }
    Ok(builder)
}

fn configure_database(
    mut builder: ConfigBuilder<DefaultState>,
    env: impl Fn(&str) -> Option<String>,
) -> Result<ConfigBuilder<DefaultState>, ConfigError> {
    for (key, name, default) in DATABASE_SETTINGS {
        builder = builder.set_default(key, default)?;
        if let Some(value) = env(name) {
            builder = builder.set_override(key, value)?;
        }
    }
    Ok(builder)
}

fn configure_settings(
    mut builder: ConfigBuilder<DefaultState>,
    settings: &[(&str, &str, &str)],
    env: impl Fn(&str) -> Option<String>,
) -> Result<ConfigBuilder<DefaultState>, ConfigError> {
    for (key, name, default) in settings {
        builder = builder.set_default(*key, *default)?;
        if let Some(value) = env(name) {
            builder = builder.set_override(*key, value)?;
        }
    }
    Ok(builder)
}

fn configure_reconciler(
    mut builder: ConfigBuilder<DefaultState>,
    env: impl Fn(&str) -> Option<String>,
) -> Result<ConfigBuilder<DefaultState>, ConfigError> {
    for (key, name, default) in RECONCILER_SETTINGS {
        builder = builder.set_default(key, default)?;
        if let Some(value) = env(name) {
            builder = builder.set_override(key, value)?;
        }
    }
    Ok(builder)
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

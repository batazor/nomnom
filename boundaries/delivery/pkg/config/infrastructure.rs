//! Конфигурация инфраструктуры процесса: Kafka, Outbox relay, PostgreSQL,
//! reconciliation и SpiceDB.

use std::time::Duration;

use config::ConfigError;
use serde::Deserialize;

use super::{
    KAFKA_BROKERS_ENV, KAFKA_CLIENT_ID_ENV, MAX_RECONCILER_PAGE_SIZE, SPICEDB_ENDPOINT_ENV,
    SPICEDB_PRESHARED_KEY_ENV,
};

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
    pub(super) fn validate(&self) -> Result<(), ConfigError> {
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

    pub(super) fn validate(&self) -> Result<(), ConfigError> {
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
    pub(super) fn validate(&self) -> Result<(), ConfigError> {
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

    pub(super) fn validate(&self) -> Result<(), ConfigError> {
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

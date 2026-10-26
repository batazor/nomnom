//! Конфигурация инфраструктуры процесса: PostgreSQL,
//! reconciliation, истечение Delivery Offers, Dispatch Deliveries и SpiceDB.

use std::time::Duration;

use config::ConfigError;
use serde::Deserialize;

use super::{
    MAX_OFFERS_DISPATCH_BATCH, MAX_OFFERS_EXPIRY_BATCH, MAX_RECONCILER_PAGE_SIZE,
    OFFERS_DISPATCH_BATCH_ENV, OFFERS_DISPATCH_INTERVAL_ENV, OFFERS_EXPIRY_BATCH_ENV,
    OFFERS_EXPIRY_INTERVAL_ENV, SPICEDB_ENDPOINT_ENV, SPICEDB_PRESHARED_KEY_ENV,
};

/// Хранилище `delivery`. URL обязателен для процесса и миграций: пустое
/// значение завершает startup (см. `di::store`). URL приходит из Secret.
///
/// Пул процесса ограничен: размер задаёт Helm (`database.pool.maxConns`, ~10);
/// миграции при старте берут отдельное соединение (`MIGRATE_MAX_CONNS`).
/// Pooler перед PostgreSQL работает только в session mode: миграции держат
/// session-level advisory lock.
#[derive(Clone, Deserialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct DatabaseConfig {
    /// libpq connection string или URL `postgres://user:password@host/db`.
    pub url: String,
    /// Максимум соединений пула процесса (1..=[`MAX_DATABASE_MAX_CONNS`]).
    pub max_conns: u32,
    /// Минимум простаивающих соединений (0..=[`MAX_DATABASE_MIN_CONNS`]):
    /// idle-соединения не держат серверные соединения session-mode pooler.
    pub min_conns: u32,
    /// Соединение старше закрывается при возврате в пул.
    #[serde(with = "humantime_serde")]
    pub max_lifetime: Duration,
    /// Простаивающее дольше соединение закрывается (выше `min_conns`).
    #[serde(with = "humantime_serde")]
    pub idle_timeout: Duration,
    /// Сколько операция ждёт соединение пула; затем ошибка `PoolTimedOut`
    /// (UNAVAILABLE клиенту) и `db.client.connection.timeouts`.
    #[serde(with = "humantime_serde")]
    pub acquire_timeout: Duration,
}

/// Верхняя граница пула одного процесса.
pub const MAX_DATABASE_MAX_CONNS: u32 = 100;
/// Верхняя граница `min_conns` (session-mode pooler).
pub const MAX_DATABASE_MIN_CONNS: u32 = 1;

impl Default for DatabaseConfig {
    fn default() -> Self {
        Self {
            url: String::new(),
            max_conns: 10,
            min_conns: 0,
            max_lifetime: Duration::from_secs(30 * 60),
            idle_timeout: Duration::from_secs(5 * 60),
            acquire_timeout: Duration::from_secs(5),
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
            .field("min_conns", &self.min_conns)
            .field("max_lifetime", &self.max_lifetime)
            .field("idle_timeout", &self.idle_timeout)
            .field("acquire_timeout", &self.acquire_timeout)
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
        let invalid = |message: String| Err(ConfigError::Message(message));
        if self.max_conns == 0 || self.max_conns > MAX_DATABASE_MAX_CONNS {
            return invalid(format!(
                "database max_conns must be between 1 and {MAX_DATABASE_MAX_CONNS}, got {}",
                self.max_conns
            ));
        }
        if self.min_conns > MAX_DATABASE_MIN_CONNS {
            return invalid(format!(
                "database min_conns must be between 0 and {MAX_DATABASE_MIN_CONNS}, got {}",
                self.min_conns
            ));
        }
        if self.min_conns > self.max_conns {
            return invalid(format!(
                "database min_conns {} must not exceed max_conns {}",
                self.min_conns, self.max_conns
            ));
        }
        for (name, value) in [
            ("max_lifetime", self.max_lifetime),
            ("idle_timeout", self.idle_timeout),
            ("acquire_timeout", self.acquire_timeout),
        ] {
            if value.is_zero() {
                return invalid(format!("database {name} must be positive"));
            }
        }
        if self.idle_timeout > self.max_lifetime {
            return invalid(format!(
                "database idle_timeout {:?} must not exceed max_lifetime {:?}",
                self.idle_timeout, self.max_lifetime
            ));
        }
        Ok(())
    }
}

/// Фоновые задачи Delivery Offers процесса `delivery`: Expire Delivery
/// Offers каждые `expiry_interval` закрывает pending offers с прошедшим
/// `expires_at` пачками по `expiry_batch`; Dispatch Deliveries каждые
/// `dispatch_interval` предлагает ожидающие доставки курьерам пачками по
/// `dispatch_batch`.
#[derive(Clone, Copy, Debug, Deserialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct OffersConfig {
    /// Интервал между проходами истечения.
    #[serde(with = "humantime_serde")]
    pub expiry_interval: Duration,
    /// Максимум offers одной пачки (одна Unit of Work на offer).
    pub expiry_batch: u32,
    /// Интервал между проходами Dispatch Deliveries.
    #[serde(with = "humantime_serde")]
    pub dispatch_interval: Duration,
    /// Максимум доставок одной пачки Dispatch (одна Unit of Work на доставку).
    pub dispatch_batch: u32,
}

impl Default for OffersConfig {
    fn default() -> Self {
        Self {
            expiry_interval: Duration::from_secs(15),
            expiry_batch: 100,
            dispatch_interval: Duration::from_secs(2),
            dispatch_batch: 100,
        }
    }
}

impl OffersConfig {
    pub(super) fn validate(&self) -> Result<(), ConfigError> {
        if self.expiry_interval.is_zero() {
            return Err(ConfigError::Message(format!(
                "{OFFERS_EXPIRY_INTERVAL_ENV} must be positive"
            )));
        }
        if self.expiry_batch == 0 || self.expiry_batch > MAX_OFFERS_EXPIRY_BATCH {
            return Err(ConfigError::Message(format!(
                "{OFFERS_EXPIRY_BATCH_ENV} must be between 1 and {MAX_OFFERS_EXPIRY_BATCH}"
            )));
        }
        if self.dispatch_interval.is_zero() {
            return Err(ConfigError::Message(format!(
                "{OFFERS_DISPATCH_INTERVAL_ENV} must be positive"
            )));
        }
        if self.dispatch_batch == 0 || self.dispatch_batch > MAX_OFFERS_DISPATCH_BATCH {
            return Err(ConfigError::Message(format!(
                "{OFFERS_DISPATCH_BATCH_ENV} must be between 1 and {MAX_OFFERS_DISPATCH_BATCH}"
            )));
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

/// Проверка прав в SpiceDB (ADR 18). Пустой `endpoint` — ошибка startup.
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

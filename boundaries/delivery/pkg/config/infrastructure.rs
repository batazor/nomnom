//! Конфигурация инфраструктуры процесса:
//! reconciliation, истечение Delivery Offers, Dispatch Deliveries и SpiceDB.

use std::time::Duration;

use config::ConfigError;
use serde::Deserialize;

use super::{
    MAX_OFFERS_DISPATCH_BATCH, MAX_OFFERS_EXPIRY_BATCH, MAX_RECONCILER_PAGE_SIZE,
    OFFERS_DISPATCH_BATCH_ENV, OFFERS_DISPATCH_INTERVAL_ENV, OFFERS_EXPIRY_BATCH_ENV,
    OFFERS_EXPIRY_INTERVAL_ENV, SPICEDB_ENDPOINT_ENV, SPICEDB_PRESHARED_KEY_ENV,
};

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

//! Конфигурация инфраструктуры процесса:
//! reconciliation и SpiceDB.

use std::time::Duration;

use config::ConfigError;
use serde::Deserialize;

use super::{
    DEFAULT_RECONCILER_GRACE, DEFAULT_RECONCILER_INTERVAL, DEFAULT_RECONCILER_PAGE_SIZE,
    DEFAULT_SPICEDB_TIMEOUT, MAX_RECONCILER_PAGE_SIZE, SPICEDB_ENDPOINT_KEY,
    SPICEDB_PRESHARED_KEY_ENV,
};

/// Фоновая сверка relationships `courier#self` и `delivery#courier` с
/// business state процесса, секция `spec.reconciler`.
#[derive(Debug, Deserialize, PartialEq, Eq)]
#[serde(deny_unknown_fields, rename_all = "camelCase", default)]
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

impl Default for ReconcilerConfig {
    fn default() -> Self {
        Self {
            interval: DEFAULT_RECONCILER_INTERVAL,
            grace: DEFAULT_RECONCILER_GRACE,
            page_size: DEFAULT_RECONCILER_PAGE_SIZE,
        }
    }
}

impl ReconcilerConfig {
    pub(super) fn validate(&self) -> Result<(), ConfigError> {
        if self.interval.is_zero() {
            return Err(ConfigError::Message(
                "spec.reconciler.interval must be positive".to_owned(),
            ));
        }
        if self.page_size == 0 || self.page_size > MAX_RECONCILER_PAGE_SIZE {
            return Err(ConfigError::Message(format!(
                "spec.reconciler.pageSize must be between 1 and {MAX_RECONCILER_PAGE_SIZE}"
            )));
        }
        Ok(())
    }
}

/// Проверка прав в SpiceDB (ADR 18), секция `spec.spicedb`. Endpoint
/// обязателен, preshared key — секрет из env `SPICEDB__PRESHARED_KEY`.
#[derive(Clone, Deserialize, PartialEq, Eq)]
#[serde(deny_unknown_fields, rename_all = "camelCase")]
pub struct SpiceDbConfig {
    /// `host:port` или URL gRPC endpoint SpiceDB.
    pub endpoint: String,
    /// Preshared key, передаваемый как `authorization: Bearer <key>`.
    #[serde(skip)]
    pub preshared_key: String,
    /// Plaintext без TLS — только для kind и локального запуска.
    #[serde(default)]
    pub insecure: bool,
    /// Deadline одного `CheckPermission`.
    #[serde(with = "humantime_serde", default = "default_spicedb_timeout")]
    pub timeout: Duration,
}

fn default_spicedb_timeout() -> Duration {
    DEFAULT_SPICEDB_TIMEOUT
}

impl Default for SpiceDbConfig {
    fn default() -> Self {
        Self {
            endpoint: String::new(),
            preshared_key: String::new(),
            insecure: false,
            timeout: DEFAULT_SPICEDB_TIMEOUT,
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
            return Err(ConfigError::Message(format!(
                "{SPICEDB_ENDPOINT_KEY} is required"
            )));
        }
        if self.preshared_key.is_empty() {
            return Err(ConfigError::Message(format!(
                "{SPICEDB_PRESHARED_KEY_ENV} is required"
            )));
        }
        if self.timeout.is_zero() {
            return Err(ConfigError::Message(
                "spec.spicedb.timeout must be positive".to_owned(),
            ));
        }
        Ok(())
    }
}

//! Temporal процессов `delivery` и `delivery-worker` (FR-708), секция
//! `spec.temporal`: адрес frontend, namespace и таймаут подключения.
//! Значений по умолчанию нет: отсутствующее, пустое или нулевое значение
//! завершает startup.

use std::time::Duration;

use config::ConfigError;
use serde::Deserialize;

pub const TEMPORAL_HOST_PORT_KEY: &str = "spec.temporal.hostPort";
pub const TEMPORAL_NAMESPACE_KEY: &str = "spec.temporal.namespace";
pub const TEMPORAL_CONNECT_TIMEOUT_KEY: &str = "spec.temporal.connectTimeout";

#[derive(Clone, Debug, Deserialize, PartialEq, Eq)]
#[serde(deny_unknown_fields, rename_all = "camelCase")]
pub struct TemporalConfig {
    /// `host:port` frontend Temporal.
    pub host_port: String,
    pub namespace: String,
    /// Таймаут установки соединения при старте.
    #[serde(with = "humantime_serde")]
    pub connect_timeout: Duration,
}

impl TemporalConfig {
    pub(super) fn validate(&self) -> Result<(), ConfigError> {
        for (value, name) in [
            (&self.host_port, TEMPORAL_HOST_PORT_KEY),
            (&self.namespace, TEMPORAL_NAMESPACE_KEY),
        ] {
            if value.trim().is_empty() {
                return Err(ConfigError::Message(format!("{name} is required")));
            }
        }
        if self.connect_timeout.is_zero() {
            return Err(ConfigError::Message(format!(
                "{TEMPORAL_CONNECT_TIMEOUT_KEY} must be positive"
            )));
        }
        Ok(())
    }
}

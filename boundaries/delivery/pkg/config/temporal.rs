//! Temporal процессов `delivery` и `delivery-worker` (FR-708): адрес
//! frontend, namespace и таймаут подключения. Значений по умолчанию нет:
//! пустое или нулевое значение завершает startup.

use std::time::Duration;

use config::ConfigError;
use serde::Deserialize;

pub const TEMPORAL_HOST_PORT_ENV: &str = "TEMPORAL__HOST_PORT";
pub const TEMPORAL_NAMESPACE_ENV: &str = "TEMPORAL__NAMESPACE";
pub const TEMPORAL_CONNECT_TIMEOUT_ENV: &str = "TEMPORAL__CONNECT_TIMEOUT";

/// Ключ конфигурации и переменная окружения; значения по умолчанию нет.
pub(super) const TEMPORAL_SETTINGS: [(&str, &str); 3] = [
    ("temporal.host_port", TEMPORAL_HOST_PORT_ENV),
    ("temporal.namespace", TEMPORAL_NAMESPACE_ENV),
    ("temporal.connect_timeout", TEMPORAL_CONNECT_TIMEOUT_ENV),
];

#[derive(Clone, Debug, Deserialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
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
            (&self.host_port, TEMPORAL_HOST_PORT_ENV),
            (&self.namespace, TEMPORAL_NAMESPACE_ENV),
        ] {
            if value.trim().is_empty() {
                return Err(ConfigError::Message(format!("{name} is required")));
            }
        }
        if self.connect_timeout.is_zero() {
            return Err(ConfigError::Message(format!(
                "{TEMPORAL_CONNECT_TIMEOUT_ENV} must be positive"
            )));
        }
        Ok(())
    }
}

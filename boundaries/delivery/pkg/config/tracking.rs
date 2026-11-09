//! Live tracking (FR-658, FR-661, FR-665, FR-666), секция `spec.tracking`:
//! Virtual City и интервал записи позиции курьера; Valkey потока позиций —
//! секрет `VALKEY__URL` из env. Значений по умолчанию нет: отсутствующее или
//! нечисловое значение завершает startup. Правила сетки (не меньше 2
//! кварталов, положительный квартал, рамка в диапазонах широты и долготы)
//! проверяет только сам Virtual City при сборке процесса — тоже до открытия
//! портов.

use std::time::Duration;

use config::ConfigError;
use serde::Deserialize;

pub const TRACKING_PERSIST_INTERVAL_KEY: &str = "spec.tracking.persistInterval";
pub const VALKEY_URL_ENV: &str = "VALKEY__URL";

#[derive(Clone, Copy, Debug, Deserialize, PartialEq)]
#[serde(deny_unknown_fields, rename_all = "camelCase")]
pub struct TrackingConfig {
    /// Широта юго-западного угла Virtual City.
    pub city_origin_latitude: f64,
    /// Долгота юго-западного угла Virtual City.
    pub city_origin_longitude: f64,
    /// Кварталов по стороне сетки.
    pub city_blocks: u32,
    /// Сторона квартала в метрах.
    pub block_meters: f64,
    /// Минимальный интервал записи позиции курьера в PostgreSQL.
    #[serde(with = "humantime_serde")]
    pub persist_interval: Duration,
}

impl TrackingConfig {
    pub(super) fn validate(&self) -> Result<(), ConfigError> {
        if self.persist_interval.is_zero() {
            return Err(ConfigError::Message(format!(
                "{TRACKING_PERSIST_INTERVAL_KEY} must be positive"
            )));
        }
        Ok(())
    }

    /// `persist_interval` в целых секундах.
    #[must_use]
    pub fn persist_interval_seconds(&self) -> i64 {
        i64::try_from(self.persist_interval.as_secs()).unwrap_or(i64::MAX)
    }
}

/// Valkey потока позиций курьеров (pub/sub, не кеш). Только секрет из env:
/// секции `spec.valkey` нет.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct ValkeyConfig {
    /// `redis://user:password@host:port`; в Kubernetes только из Secret.
    pub url: String,
}

impl ValkeyConfig {
    pub(super) fn validate(&self) -> Result<(), ConfigError> {
        // Значение не попадает в сообщение: URL содержит пароль.
        if !(self.url.starts_with("redis://") || self.url.starts_with("rediss://")) {
            return Err(ConfigError::Message(format!(
                "{VALKEY_URL_ENV} must be a redis:// or rediss:// URL"
            )));
        }
        Ok(())
    }
}

//! Live tracking (FR-658, FR-661, FR-665, FR-666): Virtual City, интервал
//! записи позиции курьера и Valkey потока позиций. Значений по умолчанию
//! нет: пустое или невалидное значение завершает startup. Рамку Virtual
//! City в диапазонах широты и долготы проверяет сам Virtual City при сборке
//! процесса — тоже до открытия портов.

use std::time::Duration;

use config::ConfigError;
use serde::Deserialize;

pub const TRACKING_CITY_ORIGIN_LATITUDE_ENV: &str = "TRACKING__CITY_ORIGIN_LATITUDE";
pub const TRACKING_CITY_ORIGIN_LONGITUDE_ENV: &str = "TRACKING__CITY_ORIGIN_LONGITUDE";
pub const TRACKING_CITY_BLOCKS_ENV: &str = "TRACKING__CITY_BLOCKS";
pub const TRACKING_BLOCK_METERS_ENV: &str = "TRACKING__BLOCK_METERS";
pub const TRACKING_PERSIST_INTERVAL_ENV: &str = "TRACKING__PERSIST_INTERVAL";
pub const VALKEY_URL_ENV: &str = "VALKEY__URL";

/// Ключ конфигурации и переменная окружения; значения по умолчанию нет.
pub(super) const TRACKING_SETTINGS: [(&str, &str); 6] = [
    (
        "tracking.city_origin_latitude",
        TRACKING_CITY_ORIGIN_LATITUDE_ENV,
    ),
    (
        "tracking.city_origin_longitude",
        TRACKING_CITY_ORIGIN_LONGITUDE_ENV,
    ),
    ("tracking.city_blocks", TRACKING_CITY_BLOCKS_ENV),
    ("tracking.block_meters", TRACKING_BLOCK_METERS_ENV),
    ("tracking.persist_interval", TRACKING_PERSIST_INTERVAL_ENV),
    ("valkey.url", VALKEY_URL_ENV),
];

#[derive(Clone, Copy, Debug, Deserialize, PartialEq)]
#[serde(deny_unknown_fields)]
pub struct TrackingConfig {
    /// Широта юго-западного угла Virtual City.
    pub city_origin_latitude: f64,
    /// Долгота юго-западного угла Virtual City.
    pub city_origin_longitude: f64,
    /// Кварталов по стороне сетки, не меньше 2.
    pub city_blocks: u32,
    /// Сторона квартала в метрах.
    pub block_meters: f64,
    /// Минимальный интервал записи позиции курьера в PostgreSQL.
    #[serde(with = "humantime_serde")]
    pub persist_interval: Duration,
}

impl TrackingConfig {
    pub(super) fn validate(&self) -> Result<(), ConfigError> {
        if self.city_blocks < 2 {
            return Err(ConfigError::Message(format!(
                "{TRACKING_CITY_BLOCKS_ENV} must be at least 2"
            )));
        }
        if !self.block_meters.is_finite() || self.block_meters <= 0.0 {
            return Err(ConfigError::Message(format!(
                "{TRACKING_BLOCK_METERS_ENV} must be a positive number"
            )));
        }
        if self.persist_interval.is_zero() {
            return Err(ConfigError::Message(format!(
                "{TRACKING_PERSIST_INTERVAL_ENV} must be positive"
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

/// Valkey потока позиций курьеров (pub/sub, не кеш).
#[derive(Clone, Debug, Deserialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct ValkeyConfig {
    /// `redis://user:password@host:port`; в Kubernetes только из Secret.
    pub url: String,
}

impl ValkeyConfig {
    pub(super) fn validate(&self) -> Result<(), ConfigError> {
        if !(self.url.starts_with("redis://") || self.url.starts_with("rediss://")) {
            return Err(ConfigError::Message(format!(
                "{VALKEY_URL_ENV} must be a redis:// or rediss:// URL"
            )));
        }
        Ok(())
    }
}

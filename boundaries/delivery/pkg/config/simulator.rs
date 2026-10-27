//! Демо Simulator курьеров (FR-606, FR-667): интервал опроса, границы
//! случайной задержки действия и интервал движения курьеров. Значений по
//! умолчанию нет: пустое или невалидное значение, `MIN_DELAY > MAX_DELAY` и
//! `TRACK_INTERVAL > INTERVAL` завершают startup.

use std::time::Duration;

use config::ConfigError;
use serde::Deserialize;

pub const SIMULATOR_INTERVAL_ENV: &str = "SIMULATOR__INTERVAL";
pub const SIMULATOR_MIN_DELAY_ENV: &str = "SIMULATOR__MIN_DELAY";
pub const SIMULATOR_MAX_DELAY_ENV: &str = "SIMULATOR__MAX_DELAY";
pub const SIMULATOR_TRACK_INTERVAL_ENV: &str = "SIMULATOR__TRACK_INTERVAL";

/// Ключ конфигурации и переменная окружения; значения по умолчанию нет.
pub(super) const SIMULATOR_SETTINGS: [(&str, &str); 4] = [
    ("simulator.interval", SIMULATOR_INTERVAL_ENV),
    ("simulator.min_delay", SIMULATOR_MIN_DELAY_ENV),
    ("simulator.max_delay", SIMULATOR_MAX_DELAY_ENV),
    ("simulator.track_interval", SIMULATOR_TRACK_INTERVAL_ENV),
];

#[derive(Clone, Copy, Debug, Deserialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct SimulatorConfig {
    /// Интервал между проходами Simulator.
    #[serde(with = "humantime_serde")]
    pub interval: Duration,
    /// Нижняя граница задержки действия от момента, когда Simulator увидел сущность.
    #[serde(with = "humantime_serde")]
    pub min_delay: Duration,
    /// Верхняя граница задержки действия.
    #[serde(with = "humantime_serde")]
    pub max_delay: Duration,
    /// Интервал между позициями движущихся курьеров, не больше `interval`.
    #[serde(with = "humantime_serde")]
    pub track_interval: Duration,
}

impl SimulatorConfig {
    pub(super) fn validate(&self) -> Result<(), ConfigError> {
        for (value, name) in [
            (self.interval, SIMULATOR_INTERVAL_ENV),
            (self.min_delay, SIMULATOR_MIN_DELAY_ENV),
            (self.max_delay, SIMULATOR_MAX_DELAY_ENV),
            (self.track_interval, SIMULATOR_TRACK_INTERVAL_ENV),
        ] {
            if value.is_zero() {
                return Err(ConfigError::Message(format!("{name} must be positive")));
            }
        }
        if self.min_delay > self.max_delay {
            return Err(ConfigError::Message(format!(
                "{SIMULATOR_MIN_DELAY_ENV} must not exceed {SIMULATOR_MAX_DELAY_ENV}"
            )));
        }
        if self.track_interval > self.interval {
            return Err(ConfigError::Message(format!(
                "{SIMULATOR_TRACK_INTERVAL_ENV} must not exceed {SIMULATOR_INTERVAL_ENV}"
            )));
        }
        Ok(())
    }
}

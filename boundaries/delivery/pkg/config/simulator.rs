//! Демо Simulator курьеров (FR-606, FR-667), секция `spec.simulator`:
//! интервал опроса, границы случайной задержки действия и интервал движения
//! курьеров. Значений по умолчанию нет: отсутствующее или невалидное
//! значение, `minDelay > maxDelay` и `trackInterval > interval` завершают
//! startup.

use std::time::Duration;

use config::ConfigError;
use serde::Deserialize;

pub const SIMULATOR_INTERVAL_KEY: &str = "spec.simulator.interval";
pub const SIMULATOR_MIN_DELAY_KEY: &str = "spec.simulator.minDelay";
pub const SIMULATOR_MAX_DELAY_KEY: &str = "spec.simulator.maxDelay";
pub const SIMULATOR_TRACK_INTERVAL_KEY: &str = "spec.simulator.trackInterval";

#[derive(Clone, Copy, Debug, Deserialize, PartialEq, Eq)]
#[serde(deny_unknown_fields, rename_all = "camelCase")]
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
            (self.interval, SIMULATOR_INTERVAL_KEY),
            (self.min_delay, SIMULATOR_MIN_DELAY_KEY),
            (self.max_delay, SIMULATOR_MAX_DELAY_KEY),
            (self.track_interval, SIMULATOR_TRACK_INTERVAL_KEY),
        ] {
            if value.is_zero() {
                return Err(ConfigError::Message(format!("{name} must be positive")));
            }
        }
        if self.min_delay > self.max_delay {
            return Err(ConfigError::Message(format!(
                "{SIMULATOR_MIN_DELAY_KEY} must not exceed {SIMULATOR_MAX_DELAY_KEY}"
            )));
        }
        if self.track_interval > self.interval {
            return Err(ConfigError::Message(format!(
                "{SIMULATOR_TRACK_INTERVAL_KEY} must not exceed {SIMULATOR_INTERVAL_KEY}"
            )));
        }
        Ok(())
    }
}

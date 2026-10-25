//! Демо Simulator курьеров (FR-606): интервал опроса и границы случайной
//! задержки действия. Значений по умолчанию нет: пустое или невалидное
//! значение и `MIN_DELAY > MAX_DELAY` завершают startup.

use std::time::Duration;

use config::ConfigError;
use serde::Deserialize;

pub const SIMULATOR_INTERVAL_ENV: &str = "SIMULATOR__INTERVAL";
pub const SIMULATOR_MIN_DELAY_ENV: &str = "SIMULATOR__MIN_DELAY";
pub const SIMULATOR_MAX_DELAY_ENV: &str = "SIMULATOR__MAX_DELAY";

/// Ключ конфигурации и переменная окружения; значения по умолчанию нет.
pub(super) const SIMULATOR_SETTINGS: [(&str, &str); 3] = [
    ("simulator.interval", SIMULATOR_INTERVAL_ENV),
    ("simulator.min_delay", SIMULATOR_MIN_DELAY_ENV),
    ("simulator.max_delay", SIMULATOR_MAX_DELAY_ENV),
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
}

impl SimulatorConfig {
    pub(super) fn validate(&self) -> Result<(), ConfigError> {
        for (value, name) in [
            (self.interval, SIMULATOR_INTERVAL_ENV),
            (self.min_delay, SIMULATOR_MIN_DELAY_ENV),
            (self.max_delay, SIMULATOR_MAX_DELAY_ENV),
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
        Ok(())
    }
}

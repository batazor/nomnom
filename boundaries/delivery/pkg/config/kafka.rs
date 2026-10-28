//! Kafka consumer процесса (ADR 4, ADR 11): брокеры, consumer group и
//! начальный offset новой группы. Значений по умолчанию нет: пустое значение
//! или неизвестный начальный offset завершают startup.

use config::ConfigError;
use serde::Deserialize;

pub const KAFKA_BROKERS_ENV: &str = "KAFKA__BROKERS";
pub const KAFKA_CONSUMER_GROUP_ENV: &str = "KAFKA__CONSUMER_GROUP";
pub const KAFKA_INITIAL_OFFSET_ENV: &str = "KAFKA__INITIAL_OFFSET";

/// Ключ конфигурации и переменная окружения; значения по умолчанию нет.
pub(super) const KAFKA_SETTINGS: [(&str, &str); 3] = [
    ("kafka.brokers", KAFKA_BROKERS_ENV),
    ("kafka.consumer_group", KAFKA_CONSUMER_GROUP_ENV),
    ("kafka.initial_offset", KAFKA_INITIAL_OFFSET_ENV),
];

/// С какого конца partition читает группа без committed offset.
#[derive(Clone, Copy, Debug, Deserialize, PartialEq, Eq)]
#[serde(rename_all = "lowercase")]
pub enum InitialOffset {
    /// С самого старого сообщения в retention.
    Oldest,
    /// Только новые сообщения.
    Newest,
}

#[derive(Clone, Debug, Deserialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct KafkaConfig {
    /// `host:port` брокеров через запятую.
    pub brokers: String,
    /// Consumer group процесса; пишется в dead letters.
    pub consumer_group: String,
    pub initial_offset: InitialOffset,
}

impl KafkaConfig {
    pub(super) fn validate(&self) -> Result<(), ConfigError> {
        for (value, name) in [
            (&self.brokers, KAFKA_BROKERS_ENV),
            (&self.consumer_group, KAFKA_CONSUMER_GROUP_ENV),
        ] {
            if value.trim().is_empty() {
                return Err(ConfigError::Message(format!("{name} is required")));
            }
        }
        Ok(())
    }
}

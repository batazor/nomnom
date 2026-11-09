//! Kafka consumer процесса (ADR 4, ADR 11), секции `spec.kafka` (брокеры,
//! consumer group, начальный offset новой группы) и `spec.schemaRegistry`
//! (Schema Registry записей стрима, FR-715). Значений по умолчанию нет:
//! пустое значение, неизвестный начальный offset или URL Registry не
//! http(s) завершают startup.

use config::ConfigError;
use serde::Deserialize;

pub const KAFKA_BROKERS_KEY: &str = "spec.kafka.brokers";
pub const KAFKA_CONSUMER_GROUP_KEY: &str = "spec.kafka.consumerGroup";
pub const SCHEMA_REGISTRY_URL_KEY: &str = "spec.schemaRegistry.url";

/// С какого конца partition читает группа без committed offset.
#[derive(Clone, Copy, Debug, Deserialize, PartialEq, Eq)]
#[serde(rename_all = "lowercase")]
pub enum InitialOffset {
    /// С самого старого сообщения в retention.
    Oldest,
    /// Только новые сообщения.
    Newest,
}

/// Секция `spec.kafka`; URL Schema Registry — из секции `spec.schemaRegistry`.
#[derive(Clone, Debug, Deserialize, PartialEq, Eq)]
#[serde(deny_unknown_fields, rename_all = "camelCase")]
pub struct KafkaConfig {
    /// `host:port` брокеров через запятую.
    pub brokers: String,
    /// Consumer group процесса; пишется в dead letters.
    pub consumer_group: String,
    pub initial_offset: InitialOffset,
    /// Schema Registry: subject схемы записи сверяется с `event_type`.
    #[serde(skip)]
    pub schema_registry_url: String,
}

/// Секция `spec.schemaRegistry`.
#[derive(Clone, Debug, Deserialize, PartialEq, Eq)]
#[serde(deny_unknown_fields, rename_all = "camelCase")]
pub(super) struct SchemaRegistryConfig {
    pub url: String,
}

impl KafkaConfig {
    pub(super) fn validate(&self) -> Result<(), ConfigError> {
        for (value, name) in [
            (&self.brokers, KAFKA_BROKERS_KEY),
            (&self.consumer_group, KAFKA_CONSUMER_GROUP_KEY),
            (&self.schema_registry_url, SCHEMA_REGISTRY_URL_KEY),
        ] {
            if value.trim().is_empty() {
                return Err(ConfigError::Message(format!("{name} is required")));
            }
        }
        let url = url::Url::parse(&self.schema_registry_url)
            .map_err(|error| ConfigError::Message(format!("{SCHEMA_REGISTRY_URL_KEY}: {error}")))?;
        if !matches!(url.scheme(), "http" | "https") || url.host().is_none() {
            return Err(ConfigError::Message(format!(
                "{SCHEMA_REGISTRY_URL_KEY} must be an absolute http(s) url"
            )));
        }
        Ok(())
    }
}

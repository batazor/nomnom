//! Kafka publisher of the relay on `rdkafka` (librdkafka, ADR 31):
//! idempotent producer with `acks=all`, message key `aggregate_id`, Event
//! Envelope in headers.

use std::time::Duration;

use rdkafka::ClientConfig;
use rdkafka::error::{KafkaError, RDKafkaErrorCode};
use rdkafka::message::{Header, OwnedHeaders};
use rdkafka::producer::{FutureProducer, FutureRecord, Producer as _};
use rdkafka::util::Timeout;

use super::envelope::headers;
use super::relay::{OutboxEntry, PublishError, Publisher};
use crate::pkg::config::KafkaConfig;

pub struct KafkaPublisher {
    producer: FutureProducer,
    timeout: Duration,
}

impl KafkaPublisher {
    /// `publish_timeout` bounds the delivery of one message
    /// (`message.timeout.ms`), retries included.
    pub fn new(config: &KafkaConfig, publish_timeout: Duration) -> Result<Self, String> {
        config.require().map_err(|error| error.to_string())?;
        let producer = ClientConfig::new()
            .set("bootstrap.servers", &config.brokers)
            .set("client.id", &config.client_id)
            .set("enable.idempotence", "true")
            .set("acks", "all")
            .set(
                "message.timeout.ms",
                publish_timeout.as_millis().to_string(),
            )
            .create()
            .map_err(|error| format!("create kafka producer: {error}"))?;
        Ok(Self {
            producer,
            timeout: publish_timeout,
        })
    }

    /// Waits for messages still in flight.
    pub fn flush(&self) {
        if let Err(error) = self.producer.flush(Timeout::After(self.timeout)) {
            eprintln!("kafka flush: {error}");
        }
    }
}

fn classify(error: &KafkaError) -> &'static str {
    match error.rdkafka_error_code() {
        Some(RDKafkaErrorCode::MessageTimedOut | RDKafkaErrorCode::RequestTimedOut) => "timeout",
        Some(RDKafkaErrorCode::QueueFull) => "queue_full",
        Some(_) => "broker",
        None => "client",
    }
}

impl Publisher for KafkaPublisher {
    async fn publish(&self, entry: &OutboxEntry) -> Result<(), PublishError> {
        let envelope = headers(entry);
        let headers = envelope.iter().fold(
            OwnedHeaders::new_with_capacity(envelope.len()),
            |headers, (key, value)| {
                headers.insert(Header {
                    key,
                    value: Some(value.as_bytes()),
                })
            },
        );
        let record = FutureRecord::to(&entry.topic)
            .key(entry.aggregate_id.as_bytes())
            .payload(entry.payload.as_slice())
            .headers(headers);
        self.producer
            .send(record, Timeout::After(self.timeout))
            .await
            .map(drop)
            .map_err(|(error, _)| PublishError {
                class: classify(&error),
                message: error.to_string(),
            })
    }
}

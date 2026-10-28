//! Kafka consumer of the process (ADR 4, ADR 11, docs/dlq.md) on `rdkafka`:
//! one consumer group (`KAFKA__CONSUMER_GROUP`) reads one stream, the
//! offset of a message is committed only after its handler finished.
//!
//! Outcome of a message:
//!
//! - success, including a duplicate of the Inbox or a type the handler does
//!   not consume — commit;
//! - broken envelope or permanent error of the handler — the message goes to
//!   `<stream>.dlq` with the dead letter headers of the other consumers
//!   (`reason_poisoned`, `topic_poisoned`, `consumer_group_poisoned`), then
//!   commit;
//! - transient error — the handler is repeated with backoff until it
//!   succeeds or the process stops: the partition waits for the dependency.
//!
//! The dead letter topic is declared in advance (kind: Job
//! `dead-letter-topics`); the consumer never creates it.

mod envelope;

use std::sync::Arc;
use std::time::Duration;

use rdkafka::ClientConfig;
use rdkafka::consumer::{CommitMode, Consumer, StreamConsumer};
use rdkafka::message::{BorrowedMessage, Header, Headers, Message, OwnedHeaders};
use rdkafka::producer::{FutureProducer, FutureRecord};
use tokio::sync::watch;

use crate::pkg::config::{InitialOffset, KafkaConfig};

pub use envelope::{Envelope, EnvelopeError};

/// Suffix of the dead letter topic of a stream.
pub const DEAD_LETTER_SUFFIX: &str = ".dlq";
/// Dead letter headers, the same as those of the `orders` and `restaurants`
/// consumers.
pub const REASON_POISONED: &str = "reason_poisoned";
pub const TOPIC_POISONED: &str = "topic_poisoned";
pub const CONSUMER_GROUP_POISONED: &str = "consumer_group_poisoned";

/// First and longest pause between repeats of a transient failure.
const FIRST_RETRY: Duration = Duration::from_secs(1);
const LONGEST_RETRY: Duration = Duration::from_secs(30);
/// Pause after a failed receive (broker unreachable, rebalance error).
const RECEIVE_RETRY: Duration = Duration::from_secs(1);
/// How long a dead letter may wait for space in the producer queue.
const DEAD_LETTER_QUEUE_TIMEOUT: Duration = Duration::from_secs(10);

/// Failure of a message handler.
#[derive(Clone, Debug, Eq, PartialEq)]
pub enum HandlingError {
    /// Repeating does not help: a defect of the code, the contract or the
    /// data. The message goes to the dead letter topic.
    Permanent(String),
    /// A dependency failed; the handler is repeated.
    Transient(String),
}

/// Handler of one message: its envelope and payload. `Ok` — the offset may
/// be committed. Runs on a blocking thread: the use cases are synchronous.
pub type MessageHandler = Arc<dyn Fn(&Envelope, &[u8]) -> Result<(), HandlingError> + Send + Sync>;

/// Consumer of one stream with the producer of its dead letters.
pub struct EventConsumer {
    consumer: StreamConsumer,
    dead_letters: FutureProducer,
    group: String,
    topic: &'static str,
    /// Name of the handler in logs.
    name: &'static str,
}

impl EventConsumer {
    /// Creates the clients and subscribes the group to `topic`. Brokers are
    /// contacted later, in the background; invalid client configuration
    /// fails startup.
    pub fn subscribe(
        config: &KafkaConfig,
        topic: &'static str,
        name: &'static str,
    ) -> Result<Self, String> {
        let initial_offset = match config.initial_offset {
            InitialOffset::Oldest => "earliest",
            InitialOffset::Newest => "latest",
        };
        let consumer: StreamConsumer = ClientConfig::new()
            .set("bootstrap.servers", &config.brokers)
            .set("group.id", &config.consumer_group)
            .set("client.id", &config.consumer_group)
            .set("enable.auto.commit", "false")
            .set("auto.offset.reset", initial_offset)
            .create()
            .map_err(|error| format!("kafka consumer: {error}"))?;
        consumer
            .subscribe(&[topic])
            .map_err(|error| format!("kafka subscribe {topic}: {error}"))?;
        let dead_letters: FutureProducer = ClientConfig::new()
            .set("bootstrap.servers", &config.brokers)
            .set("client.id", &config.consumer_group)
            .set("enable.idempotence", "true")
            .create()
            .map_err(|error| format!("kafka dead letter producer: {error}"))?;
        Ok(Self {
            consumer,
            dead_letters,
            group: config.consumer_group.clone(),
            topic,
            name,
        })
    }

    /// Processes messages until `stop` is `true`; the message in progress is
    /// finished first.
    pub async fn run(self, handler: MessageHandler, mut stop: watch::Receiver<bool>) {
        loop {
            let received = tokio::select! {
                received = self.consumer.recv() => received,
                _ = stop.wait_for(|stopped| *stopped) => return,
            };
            let message = match received {
                Ok(message) => message,
                Err(error) => {
                    tracing::error!(operation = "consume", messaging.destination.name = self.topic, error.message = %error, "kafka receive failed");
                    if pause(RECEIVE_RETRY, &mut stop).await {
                        return;
                    }
                    continue;
                }
            };
            if !self.process(&message, &handler, &mut stop).await {
                return;
            }
            if let Err(error) = self.consumer.commit_message(&message, CommitMode::Async) {
                // The next commit of the partition covers this offset; a
                // redelivery is a duplicate of the Inbox.
                tracing::error!(operation = "consume", messaging.destination.name = self.topic, error.message = %error, "kafka commit failed");
            }
        }
    }

    /// `false` — the process stops before the message was handled; its
    /// offset is not committed.
    async fn process(
        &self,
        message: &BorrowedMessage<'_>,
        handler: &MessageHandler,
        stop: &mut watch::Receiver<bool>,
    ) -> bool {
        let headers = headers(message);
        let envelope = match Envelope::parse(&headers) {
            Ok(envelope) => envelope,
            Err(error) => {
                self.dead_letter(message, &headers, &error.to_string())
                    .await;
                return true;
            }
        };
        let Some(payload) = message.payload() else {
            self.dead_letter(message, &headers, "message without payload")
                .await;
            return true;
        };
        let envelope = Arc::new(envelope);
        let payload = Arc::new(payload.to_vec());
        let mut retry = FIRST_RETRY;
        loop {
            let (call, event, body) = (
                Arc::clone(handler),
                Arc::clone(&envelope),
                Arc::clone(&payload),
            );
            let outcome = tokio::task::spawn_blocking(move || call(&event, &body)).await;
            let outcome = outcome.unwrap_or_else(|error| {
                Err(HandlingError::Transient(format!(
                    "handler aborted: {error}"
                )))
            });
            match outcome {
                Ok(()) => return true,
                Err(HandlingError::Permanent(reason)) => {
                    self.dead_letter(message, &headers, &reason).await;
                    return true;
                }
                Err(HandlingError::Transient(reason)) => {
                    tracing::warn!(
                        operation = "consume",
                        messaging.destination.name = self.topic,
                        handler = self.name,
                        event_type = %envelope.event_type,
                        event_id = %envelope.event_id,
                        error.message = %reason,
                        "transient failure, retrying"
                    );
                    if pause(retry, stop).await {
                        return false;
                    }
                    retry = (retry * 2).min(LONGEST_RETRY);
                }
            }
        }
    }

    /// Publishes the message to `<stream>.dlq` with its key, payload and
    /// headers plus the dead letter headers. A failed publish is repeated:
    /// committing the offset without the dead letter would lose the message.
    async fn dead_letter(
        &self,
        message: &BorrowedMessage<'_>,
        headers: &[(String, Vec<u8>)],
        reason: &str,
    ) {
        let topic = format!("{}{DEAD_LETTER_SUFFIX}", self.topic);
        let mut retry = FIRST_RETRY;
        loop {
            let mut owned = OwnedHeaders::new_with_capacity(headers.len() + 3);
            for (key, value) in headers.iter().filter(|(key, _)| {
                ![REASON_POISONED, TOPIC_POISONED, CONSUMER_GROUP_POISONED].contains(&key.as_str())
            }) {
                owned = owned.insert(Header {
                    key,
                    value: Some(value.as_slice()),
                });
            }
            for (key, value) in [
                (REASON_POISONED, reason),
                (TOPIC_POISONED, self.topic),
                (CONSUMER_GROUP_POISONED, self.group.as_str()),
            ] {
                owned = owned.insert(Header {
                    key,
                    value: Some(value),
                });
            }
            let mut record = FutureRecord::<[u8], [u8]>::to(&topic).headers(owned);
            if let Some(payload) = message.payload() {
                record = record.payload(payload);
            }
            if let Some(key) = message.key() {
                record = record.key(key);
            }
            match self
                .dead_letters
                .send(record, DEAD_LETTER_QUEUE_TIMEOUT)
                .await
            {
                Ok(_) => {
                    tracing::warn!(
                        operation = "consume",
                        messaging.destination.name = self.topic,
                        handler = self.name,
                        partition = message.partition(),
                        offset = message.offset(),
                        error.message = %reason,
                        "integration event sent to DLQ"
                    );
                    return;
                }
                Err((error, _)) => {
                    tracing::error!(operation = "dead_letter", messaging.destination.name = %topic, error.message = %error, "dead letter publish failed, retrying");
                    tokio::time::sleep(retry).await;
                    retry = (retry * 2).min(LONGEST_RETRY);
                }
            }
        }
    }
}

/// Headers of a message; a header without a value is empty.
fn headers(message: &BorrowedMessage<'_>) -> Vec<(String, Vec<u8>)> {
    message
        .headers()
        .map(|headers| {
            headers
                .iter()
                .map(|header| {
                    (
                        header.key.to_owned(),
                        header.value.unwrap_or_default().to_vec(),
                    )
                })
                .collect()
        })
        .unwrap_or_default()
}

/// Waits `duration`; `true` when the process stops meanwhile.
async fn pause(duration: Duration, stop: &mut watch::Receiver<bool>) -> bool {
    let stopped = tokio::select! {
        () = tokio::time::sleep(duration) => false,
        _ = stop.wait_for(|stopped| *stopped) => true,
    };
    stopped || *stop.borrow()
}

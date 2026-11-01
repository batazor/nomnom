//! Kafka consumer of the process (ADR 4, ADR 11, docs/dlq.md) on `rdkafka`:
//! one consumer group (`KAFKA__CONSUMER_GROUP`) reads one stream, the
//! offset of a message is committed only after its handler finished.
//!
//! Outcome of a message:
//!
//! - success, including a duplicate of the Inbox or a type the handler does
//!   not consume — commit;
//! - broken envelope, value outside the Schema Registry wire format, schema
//!   of another `event_type` (FR-715), permanent error or panic of the handler —
//!   the message goes to
//!   `<stream>.dlq` with the dead letter headers of the other consumers
//!   (`reason_poisoned`, `topic_poisoned`, `consumer_group_poisoned`), then
//!   commit;
//! - transient error, including an unavailable Schema Registry — the step is
//!   repeated with backoff until it succeeds or the process stops: the
//!   partition waits for the dependency.
//!
//! The dead letter topic is declared in advance (kind: Job
//! `dead-letter-topics`); the consumer never creates it.

mod dead_letters;
mod envelope;
mod schemas;
mod telemetry;

use std::sync::Arc;
use std::time::{Duration, Instant, SystemTime, UNIX_EPOCH};

use rdkafka::ClientConfig;
use rdkafka::consumer::{CommitMode, Consumer, StreamConsumer};
use rdkafka::message::{BorrowedMessage, Header, Headers, Message, OwnedHeaders};
use rdkafka::producer::{FutureProducer, FutureRecord};
use tokio::sync::watch;
use tokio::task::JoinError;

use crate::pkg::config::{InitialOffset, KafkaConfig};
use telemetry::{
    CLASS_DESERIALIZATION, CLASS_PERMANENT, ConsumerTelemetry, INSTRUMENTATION, MessageLabels,
    Outcome,
};

pub use dead_letters::{DeadLetter, DeadLetters, REDRIVEN_FROM, Through};
pub use envelope::{Envelope, EnvelopeError};
pub use schemas::{SchemaLookupError, SchemaRegistry, WireFrame, parse_wire_frame};

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

/// What the handler did with a message; the offset may be committed.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum Handled {
    /// Processed, or of a type the handler does not consume.
    Processed,
    /// The Inbox already has the event.
    Duplicate,
}

/// Failure of a message handler.
#[derive(Clone, Debug, Eq, PartialEq)]
pub enum HandlingError {
    /// Repeating does not help: a defect of the code, the contract or the
    /// data. The message goes to the dead letter topic.
    Permanent(String),
    /// A dependency failed; the handler is repeated.
    Transient(String),
}

/// Handler of one message: its envelope and Protobuf payload without the wire
/// format. `Ok` — the offset may be committed. Runs on a blocking thread: the use cases are synchronous.
pub type MessageHandler =
    Arc<dyn Fn(&Envelope, &[u8]) -> Result<Handled, HandlingError> + Send + Sync>;

/// Consumer of one stream with the producer of its dead letters.
pub struct EventConsumer {
    consumer: StreamConsumer,
    dead_letters: FutureProducer,
    schemas: SchemaRegistry,
    telemetry: ConsumerTelemetry,
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
        let schemas = SchemaRegistry::new(&config.schema_registry_url)?;
        Ok(Self {
            consumer,
            dead_letters,
            schemas,
            telemetry: ConsumerTelemetry::new(&opentelemetry::global::meter(INSTRUMENTATION)),
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
            self.telemetry.consumed(self.topic, &self.group);
            let headers = headers(&message);
            let labels = MessageLabels {
                handler: self.name,
                event_type: header_text(&headers, "event_type"),
                topic: self.topic,
            };
            let started = Instant::now();
            let outcome = self
                .process(&message, &headers, &labels, &handler, &mut stop)
                .await;
            self.telemetry.finished(&labels, outcome, started.elapsed());
            if outcome == Outcome::Stopped {
                return;
            }
            if let Err(error) = self.consumer.commit_message(&message, CommitMode::Async) {
                // The next commit of the partition covers this offset; a
                // redelivery is a duplicate of the Inbox.
                tracing::error!(operation = "consume", messaging.destination.name = self.topic, error.message = %error, "kafka commit failed");
            }
        }
    }

    async fn process(
        &self,
        message: &BorrowedMessage<'_>,
        headers: &[(String, Vec<u8>)],
        labels: &MessageLabels,
        handler: &MessageHandler,
        stop: &mut watch::Receiver<bool>,
    ) -> Outcome {
        let envelope = match Envelope::parse(headers) {
            Ok(envelope) => envelope,
            Err(error) => {
                self.dead_letter(message, headers, &error.to_string()).await;
                return Outcome::DeadLettered(CLASS_DESERIALIZATION);
            }
        };
        self.telemetry
            .message_age(labels, message_age(envelope.occurred_at_ms));
        let Some(payload) = message.payload() else {
            self.dead_letter(message, headers, "message without payload")
                .await;
            return Outcome::DeadLettered(CLASS_DESERIALIZATION);
        };
        let frame = match parse_wire_frame(payload) {
            Ok(frame) => frame,
            Err(reason) => {
                self.dead_letter(message, headers, &reason).await;
                return Outcome::DeadLettered(CLASS_DESERIALIZATION);
            }
        };
        let mut retry = FIRST_RETRY;
        loop {
            match self.schemas.subjects(frame.schema_id).await {
                Ok(subjects) if subjects.contains(&envelope.event_type) => break,
                Ok(_) => {
                    let reason = format!(
                        "schema {} is not a schema of event_type {:?}",
                        frame.schema_id, envelope.event_type
                    );
                    self.dead_letter(message, headers, &reason).await;
                    return Outcome::DeadLettered(CLASS_DESERIALIZATION);
                }
                Err(SchemaLookupError::Unknown(id)) => {
                    let reason = format!("schema id {id} is unknown to the schema registry");
                    self.dead_letter(message, headers, &reason).await;
                    return Outcome::DeadLettered(CLASS_DESERIALIZATION);
                }
                Err(SchemaLookupError::Unavailable(reason)) => {
                    tracing::warn!(
                        operation = "consume",
                        messaging.destination.name = self.topic,
                        handler = self.name,
                        event_type = %envelope.event_type,
                        event_id = %envelope.event_id,
                        error.message = %reason,
                        "schema registry unavailable, retrying"
                    );
                    self.telemetry.retried(labels);
                    if pause(retry, stop).await {
                        return Outcome::Stopped;
                    }
                    retry = (retry * 2).min(LONGEST_RETRY);
                }
            }
        }
        let envelope = Arc::new(envelope);
        let payload = Arc::new(frame.payload.to_vec());
        let mut retry = FIRST_RETRY;
        loop {
            let (call, event, body) = (
                Arc::clone(handler),
                Arc::clone(&envelope),
                Arc::clone(&payload),
            );
            let outcome = tokio::task::spawn_blocking(move || call(&event, &body)).await;
            match outcome.unwrap_or_else(|error| Err(join_failure(error))) {
                Ok(Handled::Processed) => return Outcome::Processed,
                Ok(Handled::Duplicate) => return Outcome::Duplicate,
                Err(HandlingError::Permanent(reason)) => {
                    self.dead_letter(message, headers, &reason).await;
                    return Outcome::DeadLettered(CLASS_PERMANENT);
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
                    self.telemetry.retried(labels);
                    if pause(retry, stop).await {
                        return Outcome::Stopped;
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

/// Failure of the task of a handler. A panic is a defect of the code: a repeat
/// on the same record would stop the partition until a fix is released.
/// A cancelled task says nothing about the message.
fn join_failure(error: JoinError) -> HandlingError {
    if !error.is_panic() {
        return HandlingError::Transient(format!("handler aborted: {error}"));
    }
    let panic = error.into_panic();
    let detail = match (panic.downcast_ref::<&str>(), panic.downcast_ref::<String>()) {
        (Some(text), _) => text,
        (_, Some(text)) => text.as_str(),
        _ => "payload is not text",
    };
    HandlingError::Permanent(format!("handler panicked: {detail}"))
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

/// Value of a header; absent — empty.
fn header_text(headers: &[(String, Vec<u8>)], name: &str) -> String {
    headers
        .iter()
        .find(|(key, _)| key == name)
        .map(|(_, value)| String::from_utf8_lossy(value).into_owned())
        .unwrap_or_default()
}

/// `now − occurred_at`; an event from the future has no age.
fn message_age(occurred_at_ms: i64) -> Duration {
    let occurred_at =
        UNIX_EPOCH + Duration::from_millis(u64::try_from(occurred_at_ms).unwrap_or_default());
    SystemTime::now()
        .duration_since(occurred_at)
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

#[cfg(test)]
mod tests {
    use super::*;
    #[tokio::test]
    async fn a_panic_of_the_handler_is_permanent() {
        let literal = tokio::task::spawn_blocking(|| panic!("boom"))
            .await
            .unwrap_err();
        assert_eq!(
            join_failure(literal),
            HandlingError::Permanent("handler panicked: boom".to_owned())
        );

        let formatted = tokio::task::spawn_blocking(|| panic!("order {}", 7))
            .await
            .unwrap_err();
        assert_eq!(
            join_failure(formatted),
            HandlingError::Permanent("handler panicked: order 7".to_owned())
        );
    }

    #[tokio::test]
    async fn a_cancelled_handler_is_transient() {
        let task = tokio::spawn(std::future::pending::<()>());
        task.abort();
        let error = task.await.unwrap_err();
        assert!(matches!(join_failure(error), HandlingError::Transient(_)));
    }
}

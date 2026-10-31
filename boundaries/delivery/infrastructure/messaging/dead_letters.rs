//! Dead letters of the consumer group of the process (ADR 28, docs/dlq.md):
//! list, redrive and discard, the same contract as `orders-admin` and
//! `restaurants-admin`. Dead letters of other groups sharing `<stream>.dlq`
//! are passed over: each group resolves its own.
//!
//! The Kafka consumer group `<group>.dlq` tracks what is resolved: its
//! committed offset on `<stream>.dlq` is the first dead letter not yet
//! redriven or discarded. The group has no members, the commit is a simple
//! one, record by record: an interrupted command resumes after the last
//! resolved dead letter.

use std::time::{Duration, SystemTime, UNIX_EPOCH};

use rdkafka::consumer::{CommitMode, Consumer, StreamConsumer};
use rdkafka::message::{Header, Message, OwnedHeaders};
use rdkafka::producer::{FutureProducer, FutureRecord};
use rdkafka::types::RDKafkaRespErr;
use rdkafka::{ClientConfig, Offset, TopicPartitionList};

use super::{
    CONSUMER_GROUP_POISONED, DEAD_LETTER_QUEUE_TIMEOUT, DEAD_LETTER_SUFFIX, REASON_POISONED,
    TOPIC_POISONED, headers,
};
use crate::pkg::config::KafkaConfig;

/// Names the DLQ record a redriven message came from:
/// `<stream>.dlq/<partition>@<offset>`. A message that fails again carries it
/// into its new dead letter.
pub const REDRIVEN_FROM: &str = "redriven_from";
/// Written by the `orders` and `restaurants` consumers, never kept on redrive.
const DESERIALIZATION_ERROR: &str = "deserialization_error";
/// Suffix of the consumer group that tracks the resolved dead letters.
const RESOLVED_GROUP_SUFFIX: &str = ".dlq";
/// Bound of a metadata, watermark or offset call to the broker.
const BROKER_TIMEOUT: Duration = Duration::from_secs(10);

/// One record of `<stream>.dlq`.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct DeadLetter {
    pub partition: i32,
    pub offset: i64,
    /// Time of the DLQ record.
    pub dead_lettered_at: SystemTime,
    pub key: Option<Vec<u8>>,
    pub payload: Option<Vec<u8>>,
    /// The envelope of the event and the dead letter headers.
    pub headers: Vec<(String, Vec<u8>)>,
}

impl DeadLetter {
    /// Value of a header; absent or not UTF-8 — empty.
    #[must_use]
    pub fn header(&self, name: &str) -> &str {
        self.headers
            .iter()
            .find(|(key, _)| key == name)
            .and_then(|(_, value)| std::str::from_utf8(value).ok())
            .unwrap_or_default()
    }

    /// Why the consumer group rejected the record.
    #[must_use]
    pub fn reason(&self) -> &str {
        self.header(REASON_POISONED)
    }
}

/// Bounds an operation to one partition of the DLQ up to an offset inclusive.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct Through {
    pub partition: i32,
    pub offset: i64,
}

/// Dead letters of the group: reads the DLQ from the committed offset of
/// `<group>.dlq` up to the end it had when the operation reached the partition.
pub struct DeadLetters {
    consumer: StreamConsumer,
    producer: FutureProducer,
    group: String,
}

impl DeadLetters {
    /// Creates the clients; brokers are contacted by the operations.
    pub fn new(config: &KafkaConfig) -> Result<Self, String> {
        let consumer: StreamConsumer = ClientConfig::new()
            .set("bootstrap.servers", &config.brokers)
            .set(
                "group.id",
                format!("{}{RESOLVED_GROUP_SUFFIX}", config.consumer_group),
            )
            .set("client.id", &config.consumer_group)
            .set("enable.auto.commit", "false")
            .set("auto.offset.reset", "earliest")
            .create()
            .map_err(|error| format!("kafka dead letter consumer: {error}"))?;
        let producer: FutureProducer = ClientConfig::new()
            .set("bootstrap.servers", &config.brokers)
            .set("client.id", &config.consumer_group)
            .set("enable.idempotence", "true")
            .create()
            .map_err(|error| format!("kafka redrive producer: {error}"))?;
        Ok(Self {
            consumer,
            producer,
            group: config.consumer_group.clone(),
        })
    }

    /// The unresolved dead letters of the group on the DLQ of `stream`. A DLQ
    /// that does not exist has none.
    pub async fn list(&self, stream: &str) -> Result<Vec<DeadLetter>, String> {
        let mut letters = Vec::new();
        self.scan(stream, None, false, async |letter| {
            letters.push(letter);
            Ok(())
        })
        .await?;
        Ok(letters)
    }

    /// Publishes the unresolved dead letters of the group back to `stream`
    /// with their key and envelope headers, without the dead letter headers,
    /// and marks each resolved after the broker accepted it. Every consumer
    /// group of the stream receives the event again; the Inbox makes it a
    /// no-op where it is already processed. `through` `None` — every partition.
    pub async fn redrive(&self, stream: &str, through: Option<Through>) -> Result<usize, String> {
        let mut redriven = 0;
        self.scan(stream, through, true, async |letter| {
            self.publish_back(stream, &letter).await?;
            redriven += 1;
            Ok(())
        })
        .await?;
        Ok(redriven)
    }

    /// Marks the unresolved dead letters of the group on one partition up to
    /// `through` resolved without publishing them; `seen` receives each one
    /// first, so the caller records what was discarded.
    pub async fn discard(
        &self,
        stream: &str,
        through: Through,
        mut seen: impl FnMut(&DeadLetter),
    ) -> Result<usize, String> {
        let mut discarded = 0;
        self.scan(stream, Some(through), true, async |letter| {
            seen(&letter);
            discarded += 1;
            Ok(())
        })
        .await?;
        Ok(discarded)
    }

    async fn publish_back(&self, stream: &str, letter: &DeadLetter) -> Result<(), String> {
        let (partition, offset) = (letter.partition, letter.offset);
        let origin = letter.header(TOPIC_POISONED);
        if origin != stream {
            return Err(format!(
                "dead letter {partition}@{offset} came from topic {origin:?}, not {stream}"
            ));
        }
        let mut owned = OwnedHeaders::new_with_capacity(letter.headers.len() + 1);
        for (key, value) in letter.headers.iter().filter(|(key, _)| {
            ![
                REASON_POISONED,
                TOPIC_POISONED,
                CONSUMER_GROUP_POISONED,
                DESERIALIZATION_ERROR,
                REDRIVEN_FROM,
            ]
            .contains(&key.as_str())
        }) {
            owned = owned.insert(Header {
                key,
                value: Some(value.as_slice()),
            });
        }
        let source = format!("{stream}{DEAD_LETTER_SUFFIX}/{partition}@{offset}");
        owned = owned.insert(Header {
            key: REDRIVEN_FROM,
            value: Some(source.as_str()),
        });
        let mut record = FutureRecord::<[u8], [u8]>::to(stream).headers(owned);
        if let Some(payload) = &letter.payload {
            record = record.payload(payload);
        }
        if let Some(key) = &letter.key {
            record = record.key(key);
        }
        self.producer
            .send(record, DEAD_LETTER_QUEUE_TIMEOUT)
            .await
            .map(|_| ())
            .map_err(|(error, _)| {
                format!("redrive dead letter {partition}@{offset} to {stream}: {error}")
            })
    }

    /// Passes the unresolved dead letters of the group to `handle` in
    /// partition order. With `resolve` the offset of `<group>.dlq` moves past
    /// every record `handle` accepted, records of other groups included.
    async fn scan(
        &self,
        stream: &str,
        through: Option<Through>,
        resolve: bool,
        mut handle: impl AsyncFnMut(DeadLetter) -> Result<(), String>,
    ) -> Result<(), String> {
        let topic = format!("{stream}{DEAD_LETTER_SUFFIX}");
        let partitions = self.partitions(&topic)?;
        if let Some(bound) = through
            && !partitions.is_empty()
            && !partitions.contains(&bound.partition)
        {
            return Err(format!("{topic} has no partition {}", bound.partition));
        }
        for partition in partitions {
            let limit = match through {
                Some(bound) if bound.partition != partition => continue,
                Some(bound) => Some(bound.offset),
                None => None,
            };
            self.scan_partition(&topic, partition, limit, resolve, &mut handle)
                .await?;
        }
        Ok(())
    }

    async fn scan_partition(
        &self,
        topic: &str,
        partition: i32,
        limit: Option<i64>,
        resolve: bool,
        handle: &mut impl AsyncFnMut(DeadLetter) -> Result<(), String>,
    ) -> Result<(), String> {
        let (start, high) = self
            .consumer
            .fetch_watermarks(topic, partition, BROKER_TIMEOUT)
            .map_err(|error| format!("watermarks of {topic}/{partition}: {error}"))?;
        let end = limit.map_or(high, |offset| high.min(offset + 1));
        // Retention may have removed records past the committed offset.
        let next = self
            .resolved_offset(topic, partition)?
            .map_or(start, |offset| offset.max(start));
        if next >= end {
            return Ok(());
        }
        let mut assignment = TopicPartitionList::new();
        assignment
            .add_partition_offset(topic, partition, Offset::Offset(next))
            .map_err(|error| format!("assign {topic}/{partition}: {error}"))?;
        self.consumer
            .assign(&assignment)
            .map_err(|error| format!("assign {topic}/{partition}: {error}"))?;
        loop {
            let message = self
                .consumer
                .recv()
                .await
                .map_err(|error| format!("read {topic}/{partition}: {error}"))?;
            let offset = message.offset();
            let letter = DeadLetter {
                partition,
                offset,
                dead_lettered_at: UNIX_EPOCH
                    + Duration::from_millis(
                        message
                            .timestamp()
                            .to_millis()
                            .and_then(|millis| u64::try_from(millis).ok())
                            .unwrap_or_default(),
                    ),
                key: message.key().map(<[u8]>::to_vec),
                payload: message.payload().map(<[u8]>::to_vec),
                headers: headers(&message),
            };
            drop(message);
            if letter.header(CONSUMER_GROUP_POISONED) == self.group {
                handle(letter).await?;
            }
            if resolve {
                self.commit(topic, partition, offset + 1)?;
            }
            if offset + 1 >= end {
                return Ok(());
            }
        }
    }

    /// Partitions of the DLQ; a topic that does not exist has none.
    fn partitions(&self, topic: &str) -> Result<Vec<i32>, String> {
        let metadata = self
            .consumer
            .fetch_metadata(Some(topic), BROKER_TIMEOUT)
            .map_err(|error| format!("metadata of {topic}: {error}"))?;
        let Some(found) = metadata.topics().iter().find(|found| found.name() == topic) else {
            return Err(format!("metadata of {topic}: topic is not in the response"));
        };
        match found.error() {
            None => Ok(found.partitions().iter().map(|p| p.id()).collect()),
            Some(RDKafkaRespErr::RD_KAFKA_RESP_ERR_UNKNOWN_TOPIC_OR_PART) => Ok(Vec::new()),
            Some(error) => Err(format!("metadata of {topic}: {error:?}")),
        }
    }

    /// Committed offset of `<group>.dlq`; `None` — nothing resolved yet.
    fn resolved_offset(&self, topic: &str, partition: i32) -> Result<Option<i64>, String> {
        let mut wanted = TopicPartitionList::new();
        wanted.add_partition(topic, partition);
        let committed = self
            .consumer
            .committed_offsets(wanted, BROKER_TIMEOUT)
            .map_err(|error| format!("committed offset of {topic}/{partition}: {error}"))?;
        let element = committed.find_partition(topic, partition);
        match element.map(|element| element.offset()) {
            Some(Offset::Offset(offset)) => Ok(Some(offset)),
            Some(Offset::Invalid) => Ok(None),
            other => Err(format!(
                "committed offset of {topic}/{partition}: unexpected {other:?}"
            )),
        }
    }

    fn commit(&self, topic: &str, partition: i32, next: i64) -> Result<(), String> {
        let mut resolved = TopicPartitionList::new();
        resolved
            .add_partition_offset(topic, partition, Offset::Offset(next))
            .map_err(|error| format!("resolve {topic}/{partition} before {next}: {error}"))?;
        self.consumer
            .commit(&resolved, CommitMode::Sync)
            .map_err(|error| format!("resolve {topic}/{partition} before {next}: {error}"))
    }
}

//! ADR 28, docs/dlq.md: a group resolves only its own dead letters. Discard
//! and redrive move its `<group>.dlq` offset, the redriven record is the
//! original event without the dead letter headers, and the dead letters of
//! another group stay.

mod support;

use std::net::TcpListener;
use std::time::Duration;

use delivery::infrastructure::messaging::{
    CONSUMER_GROUP_POISONED, DeadLetter, DeadLetters, REASON_POISONED, REDRIVEN_FROM,
    TOPIC_POISONED, Through,
};
use delivery::pkg::config::{InitialOffset, KafkaConfig};
use rdkafka::ClientConfig;
use rdkafka::admin::{AdminClient, AdminOptions, NewTopic, TopicReplication};
use rdkafka::client::DefaultClientContext;
use rdkafka::consumer::{Consumer, StreamConsumer};
use rdkafka::message::{Header, Headers, Message, OwnedHeaders};
use rdkafka::producer::{FutureProducer, FutureRecord};
use testcontainers_modules::testcontainers::core::{IntoContainerPort, WaitFor};
use testcontainers_modules::testcontainers::runners::SyncRunner;
use testcontainers_modules::testcontainers::{GenericImage, ImageExt};

/// Apache Kafka of the Strimzi broker (`ops/kind/manifests/messaging/kafka.yaml`).
const KAFKA_IMAGE: &str = "apache/kafka";
const KAFKA_TAG: &str = "4.3.1";
const STREAM: &str = "delivery.v1.delivery-events";
const DLQ: &str = "delivery.v1.delivery-events.dlq";

/// The listener is advertised on the published port, so it is chosen up front.
fn start_kafka() -> String {
    let port = TcpListener::bind("127.0.0.1:0")
        .and_then(|listener| listener.local_addr())
        .expect("free port")
        .port();
    let broker = GenericImage::new(KAFKA_IMAGE, KAFKA_TAG)
        .with_exposed_port(9092.tcp())
        .with_wait_for(WaitFor::message_on_stdout("Kafka Server started"))
        .with_mapped_port(port, 9092.tcp())
        .with_env_var("KAFKA_NODE_ID", "1")
        .with_env_var("KAFKA_PROCESS_ROLES", "broker,controller")
        .with_env_var("KAFKA_LISTENERS", "PLAINTEXT://:9092,CONTROLLER://:9093")
        .with_env_var(
            "KAFKA_ADVERTISED_LISTENERS",
            format!("PLAINTEXT://127.0.0.1:{port}"),
        )
        .with_env_var("KAFKA_CONTROLLER_LISTENER_NAMES", "CONTROLLER")
        .with_env_var(
            "KAFKA_LISTENER_SECURITY_PROTOCOL_MAP",
            "CONTROLLER:PLAINTEXT,PLAINTEXT:PLAINTEXT",
        )
        .with_env_var("KAFKA_CONTROLLER_QUORUM_VOTERS", "1@localhost:9093")
        .with_env_var("KAFKA_OFFSETS_TOPIC_REPLICATION_FACTOR", "1")
        .with_env_var("KAFKA_TRANSACTION_STATE_LOG_REPLICATION_FACTOR", "1")
        .with_env_var("KAFKA_TRANSACTION_STATE_LOG_MIN_ISR", "1")
        .start()
        .expect("start kafka container");
    support::keep(broker);
    format!("127.0.0.1:{port}")
}

fn dead_letters(brokers: &str, group: &str) -> DeadLetters {
    DeadLetters::new(&KafkaConfig {
        brokers: brokers.to_owned(),
        consumer_group: group.to_owned(),
        initial_offset: InitialOffset::Oldest,
        schema_registry_url: "http://127.0.0.1:1".to_owned(),
    })
    .expect("dead letters")
}

async fn create_topics(brokers: &str) {
    let admin: AdminClient<DefaultClientContext> = ClientConfig::new()
        .set("bootstrap.servers", brokers)
        .create()
        .expect("admin");
    let topics = [STREAM, DLQ].map(|name| NewTopic::new(name, 1, TopicReplication::Fixed(1)));
    for created in admin
        .create_topics(&topics, &AdminOptions::new())
        .await
        .expect("create topics")
    {
        created.expect("create topic");
    }
}

async fn publish_dead_letter(producer: &FutureProducer, group: &str, event_id: &str, reason: &str) {
    let mut headers = OwnedHeaders::new();
    for (key, value) in [
        ("event_id", event_id),
        ("event_type", "nomnom.delivery.v1.DeliveryPickedUp"),
        ("aggregate_id", "delivery-1"),
        (REASON_POISONED, reason),
        (TOPIC_POISONED, STREAM),
        (CONSUMER_GROUP_POISONED, group),
    ] {
        headers = headers.insert(Header {
            key,
            value: Some(value),
        });
    }
    producer
        .send(
            FutureRecord::to(DLQ)
                .key("delivery-1")
                .payload("payload")
                .headers(headers),
            Duration::from_secs(30),
        )
        .await
        .expect("publish dead letter");
}

fn event_ids(letters: &[DeadLetter]) -> Vec<&str> {
    letters
        .iter()
        .map(|letter| letter.header("event_id"))
        .collect()
}

#[tokio::test]
async fn a_group_lists_redrives_and_discards_its_own_dead_letters() {
    let brokers = support::off_runtime(start_kafka);
    let delivery = dead_letters(&brokers, "delivery");
    assert!(
        delivery.list(STREAM).await.expect("list").is_empty(),
        "a DLQ that does not exist has no dead letters"
    );

    create_topics(&brokers).await;
    let producer: FutureProducer = ClientConfig::new()
        .set("bootstrap.servers", &brokers)
        .create()
        .expect("producer");
    publish_dead_letter(
        &producer,
        "delivery",
        "event-discarded",
        "unknown event_type",
    )
    .await;
    publish_dead_letter(&producer, "orders", "event-foreign", "permanent").await;
    publish_dead_letter(
        &producer,
        "delivery",
        "event-redriven",
        "handler panicked: boom",
    )
    .await;

    let listed = delivery.list(STREAM).await.expect("list");
    assert_eq!(event_ids(&listed), ["event-discarded", "event-redriven"]);
    assert_eq!(listed[0].reason(), "unknown event_type");
    assert_eq!(
        delivery.list(STREAM).await.expect("list again").len(),
        2,
        "listing resolves nothing"
    );

    let mut seen = Vec::new();
    let discarded = delivery
        .discard(
            STREAM,
            Through {
                partition: 0,
                offset: listed[0].offset,
            },
            |letter| seen.push(letter.header("event_id").to_owned()),
        )
        .await
        .expect("discard");
    assert_eq!(discarded, 1);
    assert_eq!(seen, ["event-discarded"]);

    assert_eq!(delivery.redrive(STREAM, None).await.expect("redrive"), 1);

    let consumer: StreamConsumer = ClientConfig::new()
        .set("bootstrap.servers", &brokers)
        .set("group.id", "delivery-dead-letters-test")
        .set("auto.offset.reset", "earliest")
        .create()
        .expect("consumer");
    consumer.subscribe(&[STREAM]).expect("subscribe");
    let record = tokio::time::timeout(Duration::from_secs(60), consumer.recv())
        .await
        .expect("redriven record")
        .expect("read redriven record");
    assert_eq!(record.key(), Some(b"delivery-1".as_slice()));
    assert_eq!(record.payload(), Some(b"payload".as_slice()));
    let headers: Vec<(String, String)> = record
        .headers()
        .expect("headers")
        .iter()
        .map(|header| {
            (
                header.key.to_owned(),
                String::from_utf8(header.value.expect("value").to_vec()).expect("utf-8"),
            )
        })
        .collect();
    let value = |name: &str| {
        headers
            .iter()
            .find(|(key, _)| key == name)
            .map(|(_, value)| value.as_str())
    };
    assert_eq!(value("event_id"), Some("event-redriven"));
    assert_eq!(
        value(REDRIVEN_FROM),
        Some("delivery.v1.delivery-events.dlq/0@2")
    );
    for removed in [REASON_POISONED, TOPIC_POISONED, CONSUMER_GROUP_POISONED] {
        assert_eq!(value(removed), None, "{removed}");
    }

    assert!(
        delivery.list(STREAM).await.expect("list").is_empty(),
        "resolved dead letters are not listed again"
    );
    let foreign = dead_letters(&brokers, "orders")
        .list(STREAM)
        .await
        .expect("list foreign");
    assert_eq!(event_ids(&foreign), ["event-foreign"]);
}

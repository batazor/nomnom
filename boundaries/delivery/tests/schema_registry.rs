//! FR-715, FR-717: subjects of a schema id from the Confluent Schema Registry
//! of the kind environment, with a Kafka broker of its own.

mod support;

use std::path::Path;

use delivery::infrastructure::messaging::{SchemaLookupError, SchemaRegistry};
use testcontainers_modules::testcontainers::core::{IntoContainerPort, WaitFor};
use testcontainers_modules::testcontainers::runners::SyncRunner;
use testcontainers_modules::testcontainers::{GenericImage, ImageExt};

/// Apache Kafka of the Strimzi broker (`ops/kind/manifests/messaging/kafka.yaml`).
const KAFKA_IMAGE: &str = "apache/kafka";
const KAFKA_TAG: &str = "4.3.1";
/// `ops/kind/manifests/messaging/schema-registry.yaml`.
const SCHEMA_REGISTRY_IMAGE: &str = "docker.io/confluentinc/cp-schema-registry";
const SCHEMA_REGISTRY_TAG: &str = "8.4.0";
const SCHEMA_REGISTRY: u16 = 8081;

/// Kafka and Schema Registry on a network of their own: the Registry stores
/// schemas in the `_schemas` topic of the broker, reached by container name.
fn start_registry() -> String {
    let id = std::process::id();
    let network = format!("delivery-schema-registry-{id}");
    let kafka = format!("delivery-kafka-{id}");
    let broker = GenericImage::new(KAFKA_IMAGE, KAFKA_TAG)
        .with_wait_for(WaitFor::message_on_stdout("Kafka Server started"))
        .with_network(&network)
        .with_container_name(&kafka)
        .with_env_var("KAFKA_NODE_ID", "1")
        .with_env_var("KAFKA_PROCESS_ROLES", "broker,controller")
        .with_env_var("KAFKA_LISTENERS", "PLAINTEXT://:9092,CONTROLLER://:9093")
        .with_env_var(
            "KAFKA_ADVERTISED_LISTENERS",
            format!("PLAINTEXT://{kafka}:9092"),
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
    let registry = GenericImage::new(SCHEMA_REGISTRY_IMAGE, SCHEMA_REGISTRY_TAG)
        .with_exposed_port(SCHEMA_REGISTRY.tcp())
        .with_network(&network)
        .with_env_var("SCHEMA_REGISTRY_HOST_NAME", "schema-registry")
        .with_env_var("SCHEMA_REGISTRY_LISTENERS", "http://0.0.0.0:8081")
        .with_env_var(
            "SCHEMA_REGISTRY_KAFKASTORE_BOOTSTRAP_SERVERS",
            format!("PLAINTEXT://{kafka}:9092"),
        )
        .start()
        .expect("start schema registry container");
    let port = registry
        .get_host_port_ipv4(SCHEMA_REGISTRY.tcp())
        .expect("schema registry port");
    support::keep(registry);
    format!("http://127.0.0.1:{port}")
}

/// Waits until the Registry answers: it starts only after the broker accepts
/// its `_schemas` topic.
async fn await_registry(url: &str) {
    for _ in 0..240 {
        if let Ok(response) = reqwest::get(format!("{url}/subjects")).await
            && response.status().is_success()
        {
            return;
        }
        tokio::time::sleep(std::time::Duration::from_millis(500)).await;
    }
    panic!("schema registry {url} did not start");
}

/// Registers `nomnom/delivery/v1/events.proto` under the subjects and returns its id.
async fn register(url: &str, subjects: &[&str]) -> u32 {
    await_registry(url).await;
    let file = Path::new(env!("CARGO_MANIFEST_DIR"))
        .join("infrastructure/grpc/nomnom/delivery/v1/events.proto");
    let schema = std::fs::read_to_string(file).expect("read events.proto");
    let body = serde_json::json!({ "schemaType": "PROTOBUF", "schema": schema }).to_string();
    let client = reqwest::Client::new();
    let mut id = 0;
    for subject in subjects {
        let response = client
            .post(format!("{url}/subjects/{subject}/versions"))
            .header("Content-Type", "application/vnd.schemaregistry.v1+json")
            .body(body.clone())
            .send()
            .await
            .expect("register schema");
        assert_eq!(response.status().as_u16(), 200, "register {subject}");
        let registered: serde_json::Value =
            serde_json::from_slice(&response.bytes().await.expect("body")).expect("json");
        id = u32::try_from(registered["id"].as_u64().expect("id")).expect("u32 id");
    }
    id
}

#[tokio::test]
async fn resolves_subjects_unknown_ids_and_unavailable_registry() {
    // The plain clients of the test need a provider; SchemaRegistry configures its own.
    let _ = rustls::crypto::ring::default_provider().install_default();
    let url = support::off_runtime(start_registry);
    let id = register(
        &url,
        &[
            "nomnom.delivery.v1.DeliveryPickedUp",
            "nomnom.delivery.v1.DeliveryCompleted",
        ],
    )
    .await;
    let registry = SchemaRegistry::new(&url).expect("registry");

    let mut subjects = registry.subjects(id).await.expect("subjects").to_vec();
    subjects.sort();
    assert_eq!(
        subjects,
        [
            "nomnom.delivery.v1.DeliveryCompleted",
            "nomnom.delivery.v1.DeliveryPickedUp"
        ]
    );
    // AC-978: an id the Registry does not know.
    assert_eq!(
        registry.subjects(id + 100).await,
        Err(SchemaLookupError::Unknown(id + 100))
    );
    // AC-979: an unavailable Registry is not an unknown schema.
    let unavailable = SchemaRegistry::new("http://127.0.0.1:1").expect("registry");
    assert!(matches!(
        unavailable.subjects(id).await,
        Err(SchemaLookupError::Unavailable(_))
    ));
}

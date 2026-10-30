//! FR-715, FR-717: subjects of a schema id from the Schema Registry of the
//! Redpanda broker of the kind environment.

mod support;

use std::path::Path;

use delivery::infrastructure::messaging::{SchemaLookupError, SchemaRegistry};
use testcontainers_modules::testcontainers::core::IntoContainerPort;
use testcontainers_modules::testcontainers::runners::SyncRunner;
use testcontainers_modules::testcontainers::{GenericImage, ImageExt};

/// Broker of `ops/kind/manifests/messaging/infrastructure.yaml`.
const REDPANDA_IMAGE: &str = "docker.io/redpandadata/redpanda";
const REDPANDA_TAG: &str = "v26.2.6";
const SCHEMA_REGISTRY: u16 = 8081;

fn start_registry() -> String {
    let container = GenericImage::new(REDPANDA_IMAGE, REDPANDA_TAG)
        .with_exposed_port(SCHEMA_REGISTRY.tcp())
        .with_cmd([
            "redpanda",
            "start",
            "--mode=dev-container",
            "--smp=1",
            "--memory=256M",
            "--overprovisioned",
            "--schema-registry-addr=0.0.0.0:8081",
        ])
        .start()
        .expect("start redpanda container");
    let port = container
        .get_host_port_ipv4(SCHEMA_REGISTRY.tcp())
        .expect("schema registry port");
    support::keep(container);
    format!("http://127.0.0.1:{port}")
}

/// Waits until the Registry answers: its log lines do not reach the log
/// wait of testcontainers.
async fn await_registry(url: &str) {
    for _ in 0..120 {
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

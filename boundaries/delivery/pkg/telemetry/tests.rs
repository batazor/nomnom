use std::collections::BTreeMap;
use std::time::Duration;

use crate::pkg::config::{LogLevel, OtlpProtocol};

use super::*;

fn config() -> TelemetryConfig {
    TelemetryConfig {
        service_name: "delivery-test".to_owned(),
        instance_id: "delivery-7d9f".to_owned(),
        endpoint: Some("http://127.0.0.1:4317".to_owned()),
        protocol: OtlpProtocol::Grpc,
        resource_attributes: BTreeMap::from([
            ("deployment.environment.name".to_owned(), "kind".to_owned()),
            ("service.version".to_owned(), "abc123".to_owned()),
        ]),
        tracing: false,
        metrics: false,
        metrics_export_interval: Duration::from_secs(60),
        log_level: LogLevel::Info,
    }
}

fn attribute(resource: &Resource, key: &'static str) -> Option<String> {
    resource
        .get(&opentelemetry::Key::from_static_str(key))
        .map(|value| value.as_str().into_owned())
}

#[test]
fn resource_comes_from_the_config() {
    let resource = resource(&config());

    assert_eq!(
        attribute(&resource, "service.namespace").as_deref(),
        Some("nomnom")
    );
    assert_eq!(
        attribute(&resource, "service.name").as_deref(),
        Some("delivery-test")
    );
    assert_eq!(
        attribute(&resource, "service.instance.id").as_deref(),
        Some("delivery-7d9f")
    );
    assert_eq!(
        attribute(&resource, "service.version").as_deref(),
        Some("abc123")
    );
    assert_eq!(
        attribute(&resource, "deployment.environment.name").as_deref(),
        Some("kind")
    );
    assert_eq!(
        attribute(&resource, "telemetry.sdk.language").as_deref(),
        Some("rust")
    );
    assert_eq!(resource.len(), 8, "no attributes from detectors of env");
}

#[test]
fn startup_resource_names_the_process() {
    let resource = startup_resource("delivery-worker");

    assert_eq!(
        attribute(&resource, "service.name").as_deref(),
        Some("delivery-worker")
    );
    assert_eq!(
        attribute(&resource, "service.namespace").as_deref(),
        Some("nomnom")
    );
    assert_eq!(attribute(&resource, "service.instance.id"), None);
}

#[test]
fn http_signal_urls_extend_the_endpoint() {
    assert_eq!(
        signal_url("http://alloy:4318", TRACES_PATH),
        "http://alloy:4318/v1/traces"
    );
    assert_eq!(
        signal_url("http://alloy:4318/", METRICS_PATH),
        "http://alloy:4318/v1/metrics"
    );
}

/// AC-1080: endpoint без `tracing.enabled` и `metrics.enabled` не включает
/// экспорт.
#[test]
fn endpoint_without_flags_starts_no_exporter() {
    let telemetry = start(&config()).expect("start telemetry");

    assert!(!telemetry.exporting());
    assert!(!telemetry.exporting_traces());
    telemetry.shutdown();
}

#[test]
fn export_without_endpoint_is_an_error() {
    let config = TelemetryConfig {
        endpoint: None,
        tracing: true,
        ..config()
    };

    let Err(error) = start(&config) else {
        panic!("exporting without an endpoint must fail");
    };

    assert!(error.contains("spec.telemetry.endpoint"), "{error}");
}

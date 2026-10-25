use std::time::Duration;

use opentelemetry::KeyValue;
use opentelemetry::trace::{Tracer as _, TracerProvider as _};
use opentelemetry_sdk::trace::{InMemorySpanExporter, SdkTracerProvider};

use super::*;

fn capture(resource: &Resource, work: impl FnOnce()) -> Vec<Value> {
    capture_json(resource, work)
}

fn resource() -> Resource {
    Resource::builder_empty()
        .with_attributes([
            KeyValue::new("service.namespace", "nomnom"),
            KeyValue::new("service.name", "delivery"),
            KeyValue::new("service.version", "1.2.3"),
            KeyValue::new("service.instance.id", "delivery-7d9f"),
            KeyValue::new("deployment.environment.name", "local"),
            KeyValue::new("telemetry.sdk.language", "rust"),
        ])
        .build()
}

#[test]
fn record_has_the_common_schema_without_trace_outside_a_span() {
    let records = capture(&resource(), || {
        tracing::warn!(
            operation = "outbox.publish",
            error.type = "timeout",
            seq = 7_i64,
            "outbox publish failed"
        );
    });

    assert_eq!(
        records,
        vec![serde_json::json!({
            "timestamp": "2026-10-25T12:34:56.789Z",
            "severity": "WARN",
            "message": "outbox publish failed",
            "service.namespace": "nomnom",
            "service.name": "delivery",
            "service.version": "1.2.3",
            "service.instance.id": "delivery-7d9f",
            "deployment.environment.name": "local",
            "operation": "outbox.publish",
            "error.type": "timeout",
            "seq": 7,
        })]
    );
}

#[test]
fn record_inside_a_span_carries_its_trace_and_span_id() {
    let exporter = InMemorySpanExporter::default();
    let provider = SdkTracerProvider::builder()
        .with_simple_exporter(exporter.clone())
        .build();
    let tracer = provider.tracer("test");
    let mut expected = (String::new(), String::new());

    let records = capture(&resource(), || {
        tracer.in_span("usecase create_delivery", |context| {
            let span = context.span();
            let span_context = span.span_context();
            expected = (
                span_context.trace_id().to_string(),
                span_context.span_id().to_string(),
            );
            tracing::info!(operation = "create_delivery", "delivery created");
        });
    });

    assert_eq!(records[0]["trace_id"], expected.0.as_str());
    assert_eq!(records[0]["span_id"], expected.1.as_str());
    assert_eq!(expected.0.len(), 32);
}

#[test]
fn missing_resource_attributes_are_omitted() {
    let records = capture(
        &Resource::builder_empty()
            .with_attribute(KeyValue::new("service.name", "delivery-relay"))
            .build(),
        || tracing::error!("boom"),
    );

    let record = records[0].as_object().expect("object");
    assert_eq!(record["service.name"], "delivery-relay");
    assert_eq!(record["severity"], "ERROR");
    assert!(!record.contains_key("service.version"));
    assert!(!record.contains_key("trace_id"));
}

#[test]
fn library_info_is_filtered_and_own_info_kept() {
    let records = capture(&resource(), || {
        tracing::info!(target: "sqlx::query", "SELECT secret FROM t");
        tracing::warn!(target: "sqlx::query", "slow statement");
        tracing::warn!(target: "h2::proto", "go away");
        tracing::info!("own info");
    });

    let messages: Vec<&str> = records
        .iter()
        .map(|record| record["message"].as_str().expect("message"))
        .collect();
    assert_eq!(messages, vec!["go away", "own info"]);
}

#[test]
fn timestamp_is_utc_with_milliseconds() {
    assert_eq!(rfc3339(UNIX_EPOCH), "1970-01-01T00:00:00.000Z");
    assert_eq!(
        rfc3339(UNIX_EPOCH + Duration::from_millis(951_782_400_001)),
        "2000-02-29T00:00:00.001Z"
    );
}

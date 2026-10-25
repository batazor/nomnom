use opentelemetry::KeyValue;
use opentelemetry::trace::{TraceContextExt, Tracer, TracerProvider};
use opentelemetry_sdk::trace::{InMemorySpanExporter, SdkTracerProvider};

use super::*;
use crate::slices::delivery::domains::delivery::DeliveryFailureCode;

fn resource() -> opentelemetry_sdk::Resource {
    opentelemetry_sdk::Resource::builder_empty()
        .with_attributes([
            KeyValue::new("service.namespace", "nomnom"),
            KeyValue::new("service.name", "delivery"),
        ])
        .build()
}

fn records(work: impl FnOnce()) -> Vec<serde_json::Value> {
    crate::pkg::telemetry::log::capture_json(&resource(), work)
}

fn report(log: &FailureLog, reason: &DeliveryFailureReason, courier: Option<&CourierId>) {
    log.record(&ReportedFailure {
        delivery_id: &DeliveryId::new("delivery-1"),
        order_id: &OrderId::new("order-1"),
        courier_id: courier,
        reason,
    });
}

#[test]
fn logs_one_structured_record_without_trace_outside_a_span() {
    let reason = DeliveryFailureReason::new("other", Some("dog  in the yard")).expect("reason");

    let records = records(|| report(&FailureLog, &reason, Some(&CourierId::new("courier-1"))));

    assert_eq!(
        records,
        vec![serde_json::json!({
            "timestamp": "2026-10-25T12:34:56.789Z",
            "severity": "INFO",
            "message": "delivery failure reported",
            "service.namespace": "nomnom",
            "service.name": "delivery",
            "operation": "delivery.failure_reported",
            "delivery_id": "delivery-1",
            "order_id": "order-1",
            "courier_id": "courier-1",
            "reason": "other",
            "detail": "dog in the yard",
        })]
    );
}

#[test]
fn courier_and_detail_are_optional() {
    let records = records(|| {
        report(
            &FailureLog,
            &DeliveryFailureReason::of(DeliveryFailureCode::NoAccess),
            None,
        );
    });

    let record = &records[0];
    assert_eq!(record["reason"], "no_access");
    assert_eq!(record["detail"], "");
    assert!(record.get("courier_id").is_none());
}

#[test]
fn active_span_gets_reason_attributes_and_log_gets_its_trace_context() {
    let exporter = InMemorySpanExporter::default();
    let provider = SdkTracerProvider::builder()
        .with_simple_exporter(exporter.clone())
        .build();
    let tracer = provider.tracer("test");
    let detail = "x".repeat(DeliveryFailureReason::MAX_DETAIL_CHARS);
    let reason = DeliveryFailureReason::new("courier_incident", Some(&detail)).expect("reason");

    let mut ids = (String::new(), String::new());
    let records = records(|| {
        ids = tracer.in_span("report_delivery_failure", |context| {
            report(&FailureLog, &reason, None);
            let span = context.span();
            let span_context = span.span_context();
            (
                span_context.trace_id().to_string(),
                span_context.span_id().to_string(),
            )
        });
    });
    let (trace_id, span_id) = ids;

    let record = &records[0];
    assert_eq!(record["trace_id"], trace_id.as_str());
    assert_eq!(record["span_id"], span_id.as_str());
    // The log keeps the whole (domain-limited) detail.
    assert_eq!(record["detail"], detail.as_str());

    let spans = exporter.get_finished_spans().expect("spans");
    let attributes = &spans[0].attributes;
    let value = |key: &str| {
        attributes
            .iter()
            .find(|kv| kv.key.as_str() == key)
            .map(|kv| kv.value.as_str().into_owned())
    };
    assert_eq!(value(SPAN_REASON).as_deref(), Some("courier_incident"));
    assert_eq!(
        value(SPAN_DETAIL).map(|detail| detail.chars().count()),
        Some(SPAN_DETAIL_CHARS)
    );
    let _ = provider.shutdown();
}

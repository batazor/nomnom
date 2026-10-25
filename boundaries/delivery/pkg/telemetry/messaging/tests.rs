use opentelemetry::trace::TracerProvider as _;
use opentelemetry_sdk::trace::{InMemorySpanExporter, SdkTracerProvider};

use super::*;

const STORED: &str = "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01";

fn tracer() -> (InMemorySpanExporter, SdkTracerProvider, BoxedTracer) {
    let exporter = InMemorySpanExporter::default();
    let provider = SdkTracerProvider::builder()
        .with_simple_exporter(exporter.clone())
        .build();
    let tracer = BoxedTracer::new(Box::new(provider.tracer(INSTRUMENTATION)));
    (exporter, provider, tracer)
}

#[test]
fn producer_span_continues_the_stored_trace_and_is_propagated() {
    let (exporter, _provider, tracer) = tracer();

    let context = start_send(
        &tracer,
        &Publication {
            topic: "delivery.v1.delivery-events",
            event_id: "4f6b0f8e-3c2a-5d1e-9a0b-1c2d3e4f5a6b",
            event_type: "nomnom.delivery.v1.DeliveryPickedUp",
            traceparent: Some(STORED),
        },
    );
    let header = traceparent_of(&context).expect("header");
    end_send(&context, None);

    let spans = exporter.get_finished_spans().expect("spans");
    let span = &spans[0];
    assert_eq!(span.name, "send delivery.v1.delivery-events");
    assert_eq!(span.span_kind, SpanKind::Producer);
    assert_eq!(
        span.span_context.trace_id().to_string(),
        "0af7651916cd43dd8448eb211c80319c"
    );
    assert_eq!(span.parent_span_id.to_string(), "b7ad6b7169203331");
    // The header names the producer span, not the stored parent.
    assert_eq!(
        header,
        format!(
            "00-0af7651916cd43dd8448eb211c80319c-{}-01",
            span.span_context.span_id()
        )
    );
    let attribute = |key: &str| {
        span.attributes
            .iter()
            .find(|kv| kv.key.as_str() == key)
            .map(|kv| kv.value.as_str().into_owned())
    };
    assert_eq!(attribute("messaging.system").as_deref(), Some("kafka"));
    assert_eq!(
        attribute("messaging.destination.name").as_deref(),
        Some("delivery.v1.delivery-events")
    );
    assert_eq!(
        attribute("messaging.operation.type").as_deref(),
        Some("send")
    );
}

#[test]
fn row_without_trace_context_starts_a_new_trace() {
    let (exporter, _provider, tracer) = tracer();
    for stored in [None, Some(""), Some("garbage")] {
        let context = start_send(
            &tracer,
            &Publication {
                topic: "delivery.v1.delivery-events",
                event_id: "e-1",
                event_type: "nomnom.delivery.v1.DeliveryFailed",
                traceparent: stored,
            },
        );
        end_send(&context, Some("timeout"));
    }

    for span in exporter.get_finished_spans().expect("spans") {
        assert_eq!(span.parent_span_id, opentelemetry::trace::SpanId::INVALID);
        assert_eq!(span.status, Status::error("timeout"));
    }
    assert_eq!(traceparent_of(&Context::new()), None);
}

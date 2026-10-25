//! Kafka producer spans of the Outbox relay by the messaging semantic
//! conventions. The Outbox row keeps the `traceparent` of the span that
//! wrote the Integration Event (the use case of the command); the relay
//! publishes it in a `SpanKind::Producer` span `send <topic>` that continues
//! that trace, and the Kafka `traceparent` header carries the producer span,
//! so a consumer continues from the publication (publish and process stay
//! distinct operations). A row without a valid `traceparent` starts a new
//! trace.

use opentelemetry::global::BoxedTracer;
use opentelemetry::trace::{SpanKind, Status, TraceContextExt as _, Tracer as _};
use opentelemetry::{Context, KeyValue};

pub use super::trace::{context_of_traceparent, traceparent_of};

/// Instrumentation scope of the relay spans.
pub const INSTRUMENTATION: &str = "nomnom.delivery.outbox";

/// Facts of one publication; no payload, no key.
pub struct Publication<'a> {
    pub topic: &'a str,
    pub event_id: &'a str,
    pub event_type: &'a str,
    /// `traceparent` stored with the Outbox row.
    pub traceparent: Option<&'a str>,
}

/// Starts the producer span of `publication`; returns its context.
#[must_use]
pub fn start_send(tracer: &BoxedTracer, publication: &Publication<'_>) -> Context {
    let parent = context_of_traceparent(publication.traceparent);
    let span = tracer
        .span_builder(format!("send {}", publication.topic))
        .with_kind(SpanKind::Producer)
        .with_attributes([
            KeyValue::new("messaging.system", "kafka"),
            KeyValue::new("messaging.operation.name", "send"),
            KeyValue::new("messaging.operation.type", "send"),
            KeyValue::new("messaging.destination.name", publication.topic.to_owned()),
            KeyValue::new("messaging.message.id", publication.event_id.to_owned()),
            KeyValue::new("nomnom.event.type", publication.event_type.to_owned()),
        ])
        .start_with_context(tracer, &parent);
    parent.with_span(span)
}

/// Ends the producer span; `error_class` is the closed class of a failed
/// publication (`timeout`, `queue_full`, `broker`, `client`).
pub fn end_send(context: &Context, error_class: Option<&'static str>) {
    let span = context.span();
    if let Some(class) = error_class {
        span.set_attribute(KeyValue::new("error.type", class));
        span.set_status(Status::error(class));
    }
    span.end();
}

#[cfg(test)]
#[path = "messaging/tests.rs"]
mod tests;

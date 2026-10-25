//! W3C Trace Context of the current span: extraction from incoming gRPC
//! metadata (HTTP/2 headers) and the `traceparent` the Outbox stores with an
//! Integration Event (ADR 13).

use std::collections::HashMap;

use opentelemetry::Context;
use opentelemetry::propagation::{Extractor, TextMapPropagator as _};
use opentelemetry::trace::TraceContextExt as _;
use opentelemetry_sdk::propagation::TraceContextPropagator;

/// W3C header and Outbox column.
const TRACEPARENT: &str = "traceparent";

struct Headers<'a>(&'a http::HeaderMap);

impl Extractor for Headers<'_> {
    fn get(&self, key: &str) -> Option<&str> {
        self.0.get(key).and_then(|value| value.to_str().ok())
    }

    fn keys(&self) -> Vec<&str> {
        self.0.keys().map(http::HeaderName::as_str).collect()
    }
}

/// Remote parent of an incoming request; an absent or malformed
/// `traceparent` is the empty context (a new trace).
#[must_use]
pub fn extract_context(headers: &http::HeaderMap) -> Context {
    TraceContextPropagator::new().extract(&Headers(headers))
}

/// `traceparent` of the current span, if the current context has a valid one.
#[must_use]
pub fn current_traceparent() -> Option<String> {
    traceparent_of(&Context::current())
}

/// `traceparent` of the span of `context`, if it has a valid one.
#[must_use]
pub fn traceparent_of(context: &Context) -> Option<String> {
    if !context.span().span_context().is_valid() {
        return None;
    }
    let mut carrier = HashMap::new();
    TraceContextPropagator::new().inject_context(context, &mut carrier);
    carrier.remove(TRACEPARENT)
}

#[cfg(test)]
mod tests {
    use opentelemetry::trace::{SpanContext, SpanId, TraceFlags, TraceId, TraceState};

    use super::*;

    const INCOMING: &str = "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01";

    fn headers(traceparent: &str) -> http::HeaderMap {
        let mut headers = http::HeaderMap::new();
        headers.insert(TRACEPARENT, traceparent.parse().expect("header"));
        headers
    }

    #[test]
    fn incoming_traceparent_round_trips() {
        let context = extract_context(&headers(INCOMING));
        let span = context.span();
        let span_context = span.span_context();
        assert!(span_context.is_remote());
        assert_eq!(
            span_context.trace_id(),
            TraceId::from_hex("0af7651916cd43dd8448eb211c80319c").expect("trace id")
        );
        assert_eq!(traceparent_of(&context).as_deref(), Some(INCOMING));
    }

    #[test]
    fn missing_or_malformed_traceparent_starts_no_trace() {
        assert_eq!(
            traceparent_of(&extract_context(&http::HeaderMap::new())),
            None
        );
        assert_eq!(traceparent_of(&extract_context(&headers("garbage"))), None);
        assert_eq!(current_traceparent(), None);
    }

    #[test]
    fn current_span_is_written() {
        let span_context = SpanContext::new(
            TraceId::from_hex("4bf92f3577b34da6a3ce929d0e0e4736").expect("trace id"),
            SpanId::from_hex("00f067aa0ba902b7").expect("span id"),
            TraceFlags::SAMPLED,
            false,
            TraceState::default(),
        );
        let _guard = Context::new()
            .with_remote_span_context(span_context)
            .attach();
        assert_eq!(
            current_traceparent().as_deref(),
            Some("00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
        );
    }
}

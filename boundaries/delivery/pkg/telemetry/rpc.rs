//! gRPC telemetry by the current RPC semantic conventions, shared by the
//! server layer (`infrastructure/grpc/tracing.rs`) and the outgoing clients
//! (SpiceDB): spans named `package.Service/Method` and the histograms
//! `rpc.server.call.duration` / `rpc.client.call.duration` (`s`, explicit
//! seconds buckets) → `rpc_server_call_duration_seconds` /
//! `rpc_client_call_duration_seconds` of the inventory (section «rpc»).
//!
//! Attributes: `rpc.system.name` = `grpc`, `rpc.method` = `Service/Method`
//! with the package, `rpc.response.status_code` = the code name (`OK`,
//! `NOT_FOUND`); a client also records `server.address`, `server.port` and,
//! on a non-`OK` code, `error.type` = the code name.

use std::time::Duration;

use opentelemetry::global::BoxedTracer;
use opentelemetry::metrics::{Histogram, Meter};
use opentelemetry::propagation::{Injector, TextMapPropagator as _};
use opentelemetry::trace::{SpanKind, Status, TraceContextExt as _, Tracer as _};
use opentelemetry::{Context, KeyValue};
use opentelemetry_sdk::propagation::TraceContextPropagator;

use super::SECONDS_BUCKETS;

/// Instrumentation scope of the gRPC telemetry.
pub const INSTRUMENTATION: &str = "nomnom.delivery.grpc";

pub const RPC_SYSTEM_NAME: &str = "rpc.system.name";
pub const RPC_METHOD: &str = "rpc.method";
pub const RPC_STATUS_CODE: &str = "rpc.response.status_code";

/// Canonical gRPC code names, indexed by the numeric code.
const CODE_NAMES: [&str; 17] = [
    "OK",
    "CANCELLED",
    "UNKNOWN",
    "INVALID_ARGUMENT",
    "DEADLINE_EXCEEDED",
    "NOT_FOUND",
    "ALREADY_EXISTS",
    "PERMISSION_DENIED",
    "RESOURCE_EXHAUSTED",
    "FAILED_PRECONDITION",
    "ABORTED",
    "OUT_OF_RANGE",
    "UNIMPLEMENTED",
    "INTERNAL",
    "UNAVAILABLE",
    "DATA_LOSS",
    "UNAUTHENTICATED",
];

/// Name of a numeric gRPC code; an unknown number is `UNKNOWN`.
#[must_use]
pub fn code_name(code: i32) -> &'static str {
    usize::try_from(code)
        .ok()
        .and_then(|index| CODE_NAMES.get(index))
        .copied()
        .unwrap_or("UNKNOWN")
}

/// Codes that are failures of the server itself (RPC semconv): the server
/// span gets status `Error`.
#[must_use]
pub fn is_server_error(code: i32) -> bool {
    matches!(code, 2 | 4 | 12 | 13 | 14 | 15)
}

/// `package.Service/Method` of the HTTP/2 path `/package.Service/Method`.
#[must_use]
pub fn method_of_path(path: &str) -> &str {
    path.trim_start_matches('/')
}

/// Call duration histograms of both sides.
#[derive(Clone)]
pub struct RpcMetrics {
    server: Histogram<f64>,
    client: Histogram<f64>,
}

impl RpcMetrics {
    /// Instruments of the global MeterProvider (no-op without telemetry).
    #[must_use]
    pub fn global() -> Self {
        Self::new(&opentelemetry::global::meter(INSTRUMENTATION))
    }

    #[must_use]
    pub fn new(meter: &Meter) -> Self {
        let histogram = |name: &'static str, description: &'static str| {
            meter
                .f64_histogram(name)
                .with_unit("s")
                .with_description(description)
                .with_boundaries(SECONDS_BUCKETS.to_vec())
                .build()
        };
        Self {
            server: histogram(
                "rpc.server.call.duration",
                "Measures the duration of inbound remote procedure calls.",
            ),
            client: histogram(
                "rpc.client.call.duration",
                "Measures the duration of outbound remote procedure calls.",
            ),
        }
    }

    /// One served call of `method` that ended with `code`.
    pub fn server_call(&self, method: &str, code: i32, elapsed: Duration) {
        self.server.record(
            elapsed.as_secs_f64(),
            &[
                KeyValue::new(RPC_SYSTEM_NAME, "grpc"),
                KeyValue::new(RPC_METHOD, method.to_owned()),
                KeyValue::new(RPC_STATUS_CODE, code_name(code)),
            ],
        );
    }

    /// One outgoing call.
    pub fn client_call(&self, call: &ClientCall, code: i32, elapsed: Duration) {
        let mut attributes = call.attributes();
        attributes.push(KeyValue::new(RPC_STATUS_CODE, code_name(code)));
        if code != 0 {
            attributes.push(KeyValue::new("error.type", code_name(code)));
        }
        self.client.record(elapsed.as_secs_f64(), &attributes);
    }
}

/// Target of an outgoing call.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct ClientCall {
    /// `package.Service/Method`.
    pub method: &'static str,
    pub server_address: String,
    pub server_port: u16,
}

impl ClientCall {
    fn attributes(&self) -> Vec<KeyValue> {
        vec![
            KeyValue::new(RPC_SYSTEM_NAME, "grpc"),
            KeyValue::new(RPC_METHOD, self.method),
            KeyValue::new("server.address", self.server_address.clone()),
            KeyValue::new("server.port", i64::from(self.server_port)),
        ]
    }

    /// Client span of the call, child of `parent`; returns the context to
    /// run the call in.
    #[must_use]
    pub fn start(&self, tracer: &BoxedTracer, parent: &Context) -> Context {
        let span = tracer
            .span_builder(self.method)
            .with_kind(SpanKind::Client)
            .with_attributes(self.attributes())
            .start_with_context(tracer, parent);
        parent.with_span(span)
    }
}

/// Ends the client span of `context` with the response `code`. The status
/// message of the dependency is not recorded: it may echo the request.
pub fn end_client_span(context: &Context, code: i32) {
    let span = context.span();
    span.set_attribute(KeyValue::new(RPC_STATUS_CODE, code_name(code)));
    if code != 0 {
        span.set_attribute(KeyValue::new("error.type", code_name(code)));
        span.set_status(Status::error(code_name(code)));
    }
    span.end();
}

struct Metadata<'a>(&'a mut tonic::metadata::MetadataMap);

impl Injector for Metadata<'_> {
    fn set(&mut self, key: &str, value: String) {
        if let (Ok(key), Ok(value)) = (
            tonic::metadata::MetadataKey::from_bytes(key.as_bytes()),
            value.parse(),
        ) {
            self.0.insert(key, value);
        }
    }
}

/// Writes the W3C `traceparent` (and `tracestate`) of `context` into
/// outgoing gRPC metadata.
pub fn inject(context: &Context, metadata: &mut tonic::metadata::MetadataMap) {
    TraceContextPropagator::new().inject_context(context, &mut Metadata(metadata));
}

#[cfg(test)]
#[path = "rpc/tests.rs"]
mod tests;

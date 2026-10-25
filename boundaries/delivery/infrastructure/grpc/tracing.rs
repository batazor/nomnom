//! Server telemetry of the gRPC API (ADR 9): a tower layer around the tonic
//! routes. Each request continues the W3C `traceparent` of its metadata (a
//! new trace without one), runs inside a `SpanKind::Server` span named
//! `package.Service/Method`, and the span is the current context while the
//! handler — and the Unit of Work it runs synchronously — executes, so the
//! Outbox stores its `traceparent` with every Integration Event. The same
//! layer records `rpc.server.call.duration` (`pkg/telemetry/rpc`); span and
//! metric use the current RPC semantic conventions (`rpc.system.name`,
//! `rpc.method` = `package.Service/Method`, `rpc.response.status_code` = the
//! code name, `error.type` on a server failure).
//!
//! The status is the `grpc-status` of a trailers-only response, which is how
//! tonic answers a handler error; a response with a body carries it in
//! trailers, where a unary handler that returned `Ok` has only `OK`.

use std::future::Future;
use std::pin::Pin;
use std::sync::Arc;
use std::task::{Context as TaskContext, Poll};
use std::time::Instant;

use opentelemetry::context::FutureExt as _;
use opentelemetry::global::BoxedTracer;
use opentelemetry::trace::{SpanKind, Status, TraceContextExt as _, Tracer as _};
use opentelemetry::{Context, KeyValue};

use crate::pkg::telemetry::extract_context;
use crate::pkg::telemetry::rpc::{self, RpcMetrics};

/// Instrumentation scope of the gRPC server spans.
pub const TRACER_NAME: &str = rpc::INSTRUMENTATION;

/// Layer of [`TracedService`].
#[derive(Clone)]
pub struct GrpcTraceLayer {
    tracer: Arc<BoxedTracer>,
    metrics: RpcMetrics,
}

impl GrpcTraceLayer {
    /// Spans and metrics of the global providers (no-op without export; the
    /// incoming context still propagates).
    #[must_use]
    pub fn global() -> Self {
        Self::with_metrics(
            opentelemetry::global::tracer(TRACER_NAME),
            RpcMetrics::global(),
        )
    }

    /// Spans of `tracer`, metrics of the global MeterProvider.
    #[must_use]
    pub fn new(tracer: BoxedTracer) -> Self {
        Self::with_metrics(tracer, RpcMetrics::global())
    }

    #[must_use]
    pub fn with_metrics(tracer: BoxedTracer, metrics: RpcMetrics) -> Self {
        Self {
            tracer: Arc::new(tracer),
            metrics,
        }
    }
}

impl<S> tower::Layer<S> for GrpcTraceLayer {
    type Service = TracedService<S>;

    fn layer(&self, inner: S) -> Self::Service {
        TracedService {
            inner,
            tracer: Arc::clone(&self.tracer),
            metrics: self.metrics.clone(),
        }
    }
}

#[derive(Clone)]
pub struct TracedService<S> {
    inner: S,
    tracer: Arc<BoxedTracer>,
    metrics: RpcMetrics,
}

/// `grpc-status` of a trailers-only response (tonic sends handler errors so);
/// a streamed body carries it in trailers, where `0` is the only value.
fn grpc_status<B>(response: &http::Response<B>) -> i32 {
    response
        .headers()
        .get("grpc-status")
        .and_then(|value| value.to_str().ok())
        .and_then(|value| value.parse().ok())
        .unwrap_or(0)
}

impl<S, B, ResBody> tower::Service<http::Request<B>> for TracedService<S>
where
    S: tower::Service<http::Request<B>, Response = http::Response<ResBody>>,
    S::Future: Send + 'static,
{
    type Response = S::Response;
    type Error = S::Error;
    type Future = Pin<Box<dyn Future<Output = Result<S::Response, S::Error>> + Send>>;

    fn poll_ready(&mut self, cx: &mut TaskContext<'_>) -> Poll<Result<(), Self::Error>> {
        self.inner.poll_ready(cx)
    }

    fn call(&mut self, request: http::Request<B>) -> Self::Future {
        let parent = extract_context(request.headers());
        let method = rpc::method_of_path(request.uri().path()).to_owned();
        let span = self
            .tracer
            .span_builder(method.clone())
            .with_kind(SpanKind::Server)
            .with_attributes([
                KeyValue::new(rpc::RPC_SYSTEM_NAME, "grpc"),
                KeyValue::new(rpc::RPC_METHOD, method.clone()),
            ])
            .start_with_context(self.tracer.as_ref(), &parent);
        let metrics = self.metrics.clone();
        let started = Instant::now();
        let context = parent.with_span(span);
        // The inner call may already run handler code: attach the span for it.
        let future = {
            let _attached = context.clone().attach();
            self.inner.call(request)
        };
        Box::pin(
            async move {
                let result = future.await;
                let context = Context::current();
                let span = context.span();
                // A transport error of the inner service never reached the
                // handler as a status: it is reported as UNKNOWN.
                let code = result.as_ref().map_or(2, grpc_status);
                span.set_attribute(KeyValue::new(rpc::RPC_STATUS_CODE, rpc::code_name(code)));
                if rpc::is_server_error(code) {
                    span.set_attribute(KeyValue::new("error.type", rpc::code_name(code)));
                    span.set_status(Status::error(rpc::code_name(code)));
                }
                span.end();
                metrics.server_call(&method, code, started.elapsed());
                result
            }
            .with_context(context),
        )
    }
}

#[cfg(test)]
mod tests;

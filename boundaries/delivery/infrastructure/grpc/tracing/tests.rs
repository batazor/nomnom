use std::convert::Infallible;
use std::sync::Mutex;

use opentelemetry::trace::{SpanId, TraceId, TracerProvider as _};
use opentelemetry_sdk::trace::{InMemorySpanExporter, SdkTracerProvider};

use super::*;
use crate::pkg::telemetry::current_traceparent;

const INCOMING: &str = "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01";

/// Inner service that records the `traceparent` current while it runs and
/// answers with `grpc-status`.
#[derive(Clone)]
struct Probe {
    seen: Arc<Mutex<Vec<Option<String>>>>,
    status: &'static str,
}

impl tower::Service<http::Request<()>> for Probe {
    type Response = http::Response<()>;
    type Error = Infallible;
    type Future = Pin<Box<dyn Future<Output = Result<Self::Response, Infallible>> + Send>>;

    fn poll_ready(&mut self, _cx: &mut TaskContext<'_>) -> Poll<Result<(), Infallible>> {
        Poll::Ready(Ok(()))
    }

    fn call(&mut self, _request: http::Request<()>) -> Self::Future {
        let seen = Arc::clone(&self.seen);
        let status = self.status;
        Box::pin(async move {
            tokio::task::yield_now().await;
            // A handler runs the Unit of Work synchronously on this task.
            seen.lock().expect("seen").push(current_traceparent());
            Ok(http::Response::builder()
                .header("grpc-status", status)
                .body(())
                .expect("response"))
        })
    }
}

struct Harness {
    provider: SdkTracerProvider,
    exporter: InMemorySpanExporter,
    service: TracedService<Probe>,
    seen: Arc<Mutex<Vec<Option<String>>>>,
}

fn harness(status: &'static str) -> Harness {
    let exporter = InMemorySpanExporter::default();
    let provider = SdkTracerProvider::builder()
        .with_simple_exporter(exporter.clone())
        .build();
    let seen = Arc::new(Mutex::new(Vec::new()));
    let layer = GrpcTraceLayer::new(BoxedTracer::new(Box::new(provider.tracer("test"))));
    let service = tower::Layer::layer(
        &layer,
        Probe {
            seen: Arc::clone(&seen),
            status,
        },
    );
    Harness {
        provider,
        exporter,
        service,
        seen,
    }
}

fn request(traceparent: Option<&str>) -> http::Request<()> {
    let mut builder = http::Request::builder()
        .uri("http://delivery/nomnom.delivery.v1.DeliveryService/CancelDelivery");
    if let Some(traceparent) = traceparent {
        builder = builder.header("traceparent", traceparent);
    }
    builder.body(()).expect("request")
}

async fn serve(h: &mut Harness, traceparent: Option<&str>) {
    tower::Service::call(&mut h.service, request(traceparent))
        .await
        .expect("response");
    h.provider.force_flush().expect("flush");
}

#[tokio::test]
async fn request_continues_the_incoming_trace_in_a_server_span() {
    let mut h = harness("0");
    serve(&mut h, Some(INCOMING)).await;

    let spans = h.exporter.get_finished_spans().expect("spans");
    assert_eq!(spans.len(), 1);
    let span = &spans[0];
    assert_eq!(
        span.name,
        "nomnom.delivery.v1.DeliveryService/CancelDelivery"
    );
    assert_eq!(span.span_kind, SpanKind::Server);
    assert_eq!(
        span.span_context.trace_id(),
        TraceId::from_hex("0af7651916cd43dd8448eb211c80319c").expect("trace id")
    );
    assert_eq!(
        span.parent_span_id,
        SpanId::from_hex("b7ad6b7169203331").expect("span id")
    );
    assert!(span.attributes.contains(&KeyValue::new(
        "rpc.method",
        "nomnom.delivery.v1.DeliveryService/CancelDelivery"
    )));
    assert!(
        span.attributes
            .contains(&KeyValue::new("rpc.system.name", "grpc"))
    );
    assert!(
        span.attributes
            .contains(&KeyValue::new("rpc.response.status_code", "OK"))
    );
    assert_eq!(span.status, Status::Unset);

    // The handler saw the server span itself as the current context.
    let seen = h.seen.lock().expect("seen").clone();
    assert_eq!(
        seen,
        vec![Some(format!(
            "00-{}-{}-01",
            span.span_context.trace_id(),
            span.span_context.span_id()
        ))]
    );
}

#[tokio::test]
async fn request_without_traceparent_starts_a_new_trace() {
    let mut h = harness("0");
    serve(&mut h, None).await;

    let spans = h.exporter.get_finished_spans().expect("spans");
    assert_eq!(spans.len(), 1);
    assert_eq!(spans[0].parent_span_id, SpanId::INVALID);
    let seen = h.seen.lock().expect("seen").clone();
    assert!(seen[0].as_deref().is_some_and(|traceparent| {
        traceparent.contains(&spans[0].span_context.trace_id().to_string())
    }));
}

#[tokio::test]
async fn server_error_status_marks_the_span() {
    let mut h = harness("14");
    serve(&mut h, Some(INCOMING)).await;
    let spans = h.exporter.get_finished_spans().expect("spans");
    assert!(matches!(spans[0].status, Status::Error { .. }));
    assert!(
        spans[0]
            .attributes
            .contains(&KeyValue::new("rpc.response.status_code", "UNAVAILABLE"))
    );

    // A client error (NOT_FOUND) is not a server failure.
    let mut h = harness("5");
    serve(&mut h, None).await;
    assert_eq!(
        h.exporter.get_finished_spans().expect("spans")[0].status,
        Status::Unset
    );
}

/// Without trace export the no-op tracer still propagates the caller's
/// context, so the Outbox keeps the incoming trace.
#[tokio::test]
async fn noop_tracer_propagates_the_incoming_context() {
    let seen = Arc::new(Mutex::new(Vec::new()));
    let layer = GrpcTraceLayer::new(BoxedTracer::new(Box::new(
        opentelemetry::trace::noop::NoopTracer::new(),
    )));
    let mut service = tower::Layer::layer(
        &layer,
        Probe {
            seen: Arc::clone(&seen),
            status: "0",
        },
    );
    tower::Service::call(&mut service, request(Some(INCOMING)))
        .await
        .expect("response");
    assert_eq!(
        seen.lock().expect("seen").clone(),
        vec![Some(INCOMING.to_owned())]
    );
}

#[tokio::test]
async fn call_duration_is_recorded_with_the_code_name() {
    use opentelemetry::metrics::MeterProvider as _;
    use opentelemetry_sdk::metrics::data::{AggregatedMetrics, MetricData};
    use opentelemetry_sdk::metrics::{InMemoryMetricExporter, PeriodicReader, SdkMeterProvider};

    let exporter = InMemoryMetricExporter::default();
    let provider = SdkMeterProvider::builder()
        .with_reader(PeriodicReader::builder(exporter.clone()).build())
        .build();
    let layer = GrpcTraceLayer::with_metrics(
        BoxedTracer::new(Box::new(opentelemetry::trace::noop::NoopTracer::new())),
        RpcMetrics::new(&provider.meter(TRACER_NAME)),
    );
    let mut service = tower::Layer::layer(
        &layer,
        Probe {
            seen: Arc::default(),
            status: "5",
        },
    );
    tower::Service::call(&mut service, request(None))
        .await
        .expect("response");
    provider.force_flush().expect("flush");

    let exported = exporter
        .get_finished_metrics()
        .expect("metrics")
        .pop()
        .expect("export");
    let metric = exported
        .scope_metrics()
        .flat_map(|scope| scope.metrics())
        .find(|metric| metric.name() == "rpc.server.call.duration")
        .expect("rpc.server.call.duration");
    assert_eq!(metric.unit(), "s");
    let AggregatedMetrics::F64(MetricData::Histogram(histogram)) = metric.data() else {
        panic!("histogram");
    };
    let point = histogram.data_points().next().expect("point");
    let mut attributes: Vec<(String, String)> = point
        .attributes()
        .map(|kv| (kv.key.to_string(), kv.value.as_str().into_owned()))
        .collect();
    attributes.sort();
    assert_eq!(
        attributes,
        vec![
            (
                "rpc.method".to_owned(),
                "nomnom.delivery.v1.DeliveryService/CancelDelivery".to_owned()
            ),
            (
                "rpc.response.status_code".to_owned(),
                "NOT_FOUND".to_owned()
            ),
            ("rpc.system.name".to_owned(), "grpc".to_owned()),
        ]
    );
}

use opentelemetry::metrics::MeterProvider as _;
use opentelemetry::trace::TracerProvider as _;
use opentelemetry_sdk::metrics::data::{AggregatedMetrics, MetricData};
use opentelemetry_sdk::metrics::{InMemoryMetricExporter, PeriodicReader, SdkMeterProvider};
use opentelemetry_sdk::trace::{InMemorySpanExporter, SdkTracerProvider};

use super::*;

const SPICEDB_CHECK: &str = "authzed.api.v1.PermissionsService/CheckPermission";

#[test]
fn code_names_are_canonical() {
    assert_eq!(code_name(0), "OK");
    assert_eq!(code_name(5), "NOT_FOUND");
    assert_eq!(code_name(16), "UNAUTHENTICATED");
    assert_eq!(code_name(42), "UNKNOWN");
    assert_eq!(code_name(-1), "UNKNOWN");
    assert!(is_server_error(13) && !is_server_error(5));
    assert_eq!(
        method_of_path("/nomnom.delivery.v1.DeliveryService/CreateDelivery"),
        "nomnom.delivery.v1.DeliveryService/CreateDelivery"
    );
}

fn point_attributes(
    metric: &opentelemetry_sdk::metrics::data::Metric,
) -> Vec<Vec<(String, String)>> {
    assert_eq!(metric.unit(), "s");
    let AggregatedMetrics::F64(MetricData::Histogram(histogram)) = metric.data() else {
        panic!("{} is an f64 histogram", metric.name());
    };
    histogram
        .data_points()
        .map(|point| {
            assert_eq!(point.bounds().collect::<Vec<_>>(), SECONDS_BUCKETS.to_vec());
            let mut pairs: Vec<(String, String)> = point
                .attributes()
                .map(|kv| (kv.key.to_string(), kv.value.as_str().into_owned()))
                .collect();
            pairs.sort();
            pairs
        })
        .collect()
}

fn pairs(expected: &[(&str, &str)]) -> Vec<(String, String)> {
    let mut pairs: Vec<(String, String)> = expected
        .iter()
        .map(|(key, value)| ((*key).to_owned(), (*value).to_owned()))
        .collect();
    pairs.sort();
    pairs
}

#[test]
fn call_durations_carry_the_inventory_attributes() {
    let exporter = InMemoryMetricExporter::default();
    let provider = SdkMeterProvider::builder()
        .with_reader(PeriodicReader::builder(exporter.clone()).build())
        .build();
    let metrics = RpcMetrics::new(&provider.meter(INSTRUMENTATION));
    let call = ClientCall {
        method: SPICEDB_CHECK,
        server_address: "spicedb.nomnom-authz.svc".to_owned(),
        server_port: 50051,
    };

    metrics.server_call(
        "nomnom.delivery.v1.DeliveryService/CreateDelivery",
        5,
        Duration::from_millis(3),
    );
    metrics.client_call(&call, 14, Duration::from_millis(2));
    provider.force_flush().expect("flush");

    let exported = exporter
        .get_finished_metrics()
        .expect("metrics")
        .pop()
        .expect("export");
    let find = |name: &str| {
        exported
            .scope_metrics()
            .flat_map(|scope| scope.metrics())
            .find(|metric| metric.name() == name)
            .unwrap_or_else(|| panic!("{name} exported"))
    };
    assert_eq!(
        point_attributes(find("rpc.server.call.duration")),
        vec![pairs(&[
            ("rpc.system.name", "grpc"),
            (
                "rpc.method",
                "nomnom.delivery.v1.DeliveryService/CreateDelivery"
            ),
            ("rpc.response.status_code", "NOT_FOUND"),
        ])]
    );
    assert_eq!(
        point_attributes(find("rpc.client.call.duration")),
        vec![pairs(&[
            ("rpc.system.name", "grpc"),
            ("rpc.method", SPICEDB_CHECK),
            ("rpc.response.status_code", "UNAVAILABLE"),
            ("error.type", "UNAVAILABLE"),
            ("server.address", "spicedb.nomnom-authz.svc"),
            ("server.port", "50051"),
        ])]
    );
}

#[test]
fn client_span_is_injected_into_metadata() {
    let exporter = InMemorySpanExporter::default();
    let provider = SdkTracerProvider::builder()
        .with_simple_exporter(exporter.clone())
        .build();
    let tracer = BoxedTracer::new(Box::new(provider.tracer(INSTRUMENTATION)));
    let call = ClientCall {
        method: SPICEDB_CHECK,
        server_address: "localhost".to_owned(),
        server_port: 50051,
    };
    let parent = Context::new().with_span(tracer.start("usecase create_delivery"));

    let context = call.start(&tracer, &parent);
    let mut metadata = tonic::metadata::MetadataMap::new();
    inject(&context, &mut metadata);
    end_client_span(&context, 7);
    parent.span().end();

    let spans = exporter.get_finished_spans().expect("spans");
    let client = spans
        .iter()
        .find(|span| span.name == SPICEDB_CHECK)
        .expect("client span");
    assert_eq!(client.span_kind, SpanKind::Client);
    assert_eq!(client.status, Status::error("PERMISSION_DENIED"));
    let span_context = &client.span_context;
    assert_eq!(
        metadata
            .get("traceparent")
            .and_then(|value| value.to_str().ok()),
        Some(
            format!(
                "00-{}-{}-01",
                span_context.trace_id(),
                span_context.span_id()
            )
            .as_str()
        )
    );
    let parent_span = spans
        .iter()
        .find(|span| span.name == "usecase create_delivery")
        .expect("parent span");
    assert_eq!(client.parent_span_id, parent_span.span_context.span_id());
}

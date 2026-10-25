use opentelemetry::metrics::MeterProvider as _;
use opentelemetry::trace::{Status, TracerProvider as _};
use opentelemetry_sdk::metrics::data::{AggregatedMetrics, MetricData};
use opentelemetry_sdk::metrics::{InMemoryMetricExporter, PeriodicReader, SdkMeterProvider};
use opentelemetry_sdk::trace::{InMemorySpanExporter, SdkTracerProvider};

use super::*;

struct Harness {
    spans: InMemorySpanExporter,
    metrics: InMemoryMetricExporter,
    meter_provider: SdkMeterProvider,
    _tracer_provider: SdkTracerProvider,
    telemetry: UseCaseTelemetry,
}

fn harness() -> Harness {
    let spans = InMemorySpanExporter::default();
    let tracer_provider = SdkTracerProvider::builder()
        .with_simple_exporter(spans.clone())
        .build();
    let metrics = InMemoryMetricExporter::default();
    let meter_provider = SdkMeterProvider::builder()
        .with_reader(PeriodicReader::builder(metrics.clone()).build())
        .build();
    let telemetry = UseCaseTelemetry::new(
        BoxedTracer::new(Box::new(tracer_provider.tracer(INSTRUMENTATION))),
        &meter_provider.meter(INSTRUMENTATION),
    );
    Harness {
        spans,
        metrics,
        meter_provider,
        _tracer_provider: tracer_provider,
        telemetry,
    }
}

fn attributes<'a>(pairs: impl Iterator<Item = &'a KeyValue>) -> Vec<(String, String)> {
    let mut pairs: Vec<(String, String)> = pairs
        .map(|kv| (kv.key.to_string(), kv.value.as_str().into_owned()))
        .collect();
    pairs.sort();
    pairs
}

fn pairs(expected: &[(&str, &str)]) -> Vec<(String, String)> {
    let mut pairs: Vec<(String, String)> = expected
        .iter()
        .map(|(key, value)| ((*key).to_owned(), (*value).to_owned()))
        .collect();
    pairs.sort();
    pairs
}

impl Harness {
    /// Attribute sets of the `nomnom.usecase.duration` data points.
    fn duration_points(&self) -> Vec<Vec<(String, String)>> {
        self.meter_provider.force_flush().expect("flush");
        let exported = self
            .metrics
            .get_finished_metrics()
            .expect("metrics")
            .pop()
            .expect("export");
        let metric = exported
            .scope_metrics()
            .flat_map(|scope| scope.metrics())
            .find(|metric| metric.name() == "nomnom.usecase.duration")
            .expect("nomnom.usecase.duration");
        assert_eq!(metric.unit(), "s");
        let AggregatedMetrics::F64(MetricData::Histogram(histogram)) = metric.data() else {
            panic!("f64 histogram");
        };
        let mut points: Vec<_> = histogram
            .data_points()
            .map(|point| {
                assert_eq!(point.bounds().collect::<Vec<_>>(), BUCKETS.to_vec());
                attributes(point.attributes())
            })
            .collect();
        points.sort();
        points
    }
}

#[test]
fn success_records_span_and_duration_without_error_type() {
    let harness = harness();
    let result: Result<u8, ()> = harness.telemetry.observe(
        UseCase::query("get_courier_current_delivery"),
        |()| unreachable!(),
        || {
            // The use case span is current inside the call.
            assert!(Context::current().span().span_context().is_valid());
            Ok(7)
        },
    );

    assert_eq!(result, Ok(7));
    let spans = harness.spans.get_finished_spans().expect("spans");
    assert_eq!(spans.len(), 1);
    assert_eq!(spans[0].name, "usecase get_courier_current_delivery");
    assert_eq!(spans[0].status, Status::Unset);
    let expected = pairs(&[
        ("nomnom.usecase.name", "get_courier_current_delivery"),
        ("nomnom.usecase.kind", "query"),
        ("nomnom.outcome", "success"),
    ]);
    assert_eq!(attributes(spans[0].attributes.iter()), expected);
    assert_eq!(harness.duration_points(), vec![expected]);
}

#[test]
fn rejection_and_error_are_classified() {
    let harness = harness();
    let classify = |error: &&str| match *error {
        "denied" => Failure::Rejected("permission_denied"),
        _ => Failure::Error("unavailable"),
    };

    let rejected: Result<(), &str> =
        harness
            .telemetry
            .observe(UseCase::command("create_delivery"), classify, || {
                Err("denied")
            });
    let failed: Result<(), &str> =
        harness
            .telemetry
            .observe(UseCase::command("create_delivery"), classify, || {
                Err("down")
            });

    assert_eq!((rejected, failed), (Err("denied"), Err("down")));
    let spans = harness.spans.get_finished_spans().expect("spans");
    // Only a technical failure is a span error.
    assert_eq!(spans[0].status, Status::Unset);
    assert_eq!(spans[1].status, Status::error("unavailable"));
    assert_eq!(
        harness.duration_points(),
        vec![
            pairs(&[
                ("nomnom.usecase.name", "create_delivery"),
                ("nomnom.usecase.kind", "command"),
                ("nomnom.outcome", "rejected"),
                ("error.type", "permission_denied"),
            ]),
            pairs(&[
                ("nomnom.usecase.name", "create_delivery"),
                ("nomnom.usecase.kind", "command"),
                ("nomnom.outcome", "error"),
                ("error.type", "unavailable"),
            ]),
        ]
    );
}

#[test]
fn span_continues_the_current_trace() {
    let harness = harness();
    let parent = opentelemetry::trace::SpanContext::new(
        opentelemetry::trace::TraceId::from_hex("4bf92f3577b34da6a3ce929d0e0e4736")
            .expect("trace id"),
        opentelemetry::trace::SpanId::from_hex("00f067aa0ba902b7").expect("span id"),
        opentelemetry::trace::TraceFlags::SAMPLED,
        true,
        opentelemetry::trace::TraceState::default(),
    );
    let _guard = Context::new()
        .with_remote_span_context(parent.clone())
        .attach();

    let _: Result<(), ()> = harness.telemetry.observe(
        UseCase::command("cancel_delivery"),
        |()| unreachable!(),
        || Ok(()),
    );

    let spans = harness.spans.get_finished_spans().expect("spans");
    assert_eq!(spans[0].span_context.trace_id(), parent.trace_id());
    assert_eq!(spans[0].parent_span_id, parent.span_id());
}

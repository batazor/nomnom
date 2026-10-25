use opentelemetry::metrics::MeterProvider as _;
use opentelemetry_sdk::metrics::data::{AggregatedMetrics, MetricData};
use opentelemetry_sdk::metrics::{InMemoryMetricExporter, PeriodicReader, SdkMeterProvider};

use super::*;

const STAT: &str = "4242 (delivery migrate) S 1 4242 4242 0 -1 4194560 2391 0 0 0 \
                    250 75 0 0 20 0 9 0 12345 123456789 3000 18446744073709551615";
const STATUS: &str = "Name:\tdelivery\nVmPeak:\t  200000 kB\nVmRSS:\t   12000 kB\nThreads:\t9\n";

#[test]
fn procfs_contents_are_parsed() {
    assert_eq!(
        parse(STAT, STATUS),
        Some(ProcessSample {
            cpu_user_seconds: 2.5,
            cpu_system_seconds: 0.75,
            memory_rss_bytes: 12_000 * 1024,
            threads: 9,
        })
    );
    assert_eq!(parse("garbage", STATUS), None);
    assert_eq!(parse(STAT, "Name:\tdelivery\n"), None);
}

#[cfg(target_os = "linux")]
#[test]
fn procfs_of_this_process_is_readable() {
    let sample = procfs()().expect("procfs sample");
    assert!(sample.threads >= 1);
    assert!(sample.memory_rss_bytes > 0);
}

#[test]
fn instruments_follow_process_semantic_conventions() {
    let exporter = InMemoryMetricExporter::default();
    let provider = SdkMeterProvider::builder()
        .with_reader(PeriodicReader::builder(exporter.clone()).build())
        .build();
    register(
        &provider.meter(METER_NAME),
        Arc::new(|| {
            Some(ProcessSample {
                cpu_user_seconds: 2.5,
                cpu_system_seconds: 0.75,
                memory_rss_bytes: 4096,
                threads: 3,
            })
        }),
    );
    provider.force_flush().expect("flush");
    let exported = exporter
        .get_finished_metrics()
        .expect("metrics")
        .pop()
        .expect("one export");
    let metrics: Vec<_> = exported
        .scope_metrics()
        .flat_map(|scope| scope.metrics())
        .collect();
    let find = |name: &str| {
        metrics
            .iter()
            .find(|metric| metric.name() == name)
            .unwrap_or_else(|| panic!("{name} exported"))
    };

    let cpu = find("process.cpu.time");
    assert_eq!(cpu.unit(), "s");
    let AggregatedMetrics::F64(MetricData::Sum(sum)) = cpu.data() else {
        panic!("process.cpu.time is an f64 counter");
    };
    assert!(sum.is_monotonic());
    let mut modes: Vec<(String, f64)> = sum
        .data_points()
        .map(|point| {
            let mode = point
                .attributes()
                .find(|kv| kv.key.as_str() == "cpu.mode")
                .map(|kv| kv.value.as_str().into_owned())
                .expect("cpu.mode");
            (mode, point.value())
        })
        .collect();
    modes.sort_by(|a, b| a.0.cmp(&b.0));
    assert_eq!(
        modes,
        vec![("system".to_owned(), 0.75), ("user".to_owned(), 2.5)]
    );

    for (name, unit, value) in [
        ("process.memory.usage", "By", 4096),
        ("process.thread.count", "{thread}", 3),
    ] {
        let metric = find(name);
        assert_eq!(metric.unit(), unit);
        let AggregatedMetrics::I64(MetricData::Sum(sum)) = metric.data() else {
            panic!("{name} is an i64 up-down counter");
        };
        assert!(!sum.is_monotonic());
        let points: Vec<i64> = sum.data_points().map(|point| point.value()).collect();
        assert_eq!(points, vec![value]);
    }
}

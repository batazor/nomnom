use std::time::Duration;

use opentelemetry::metrics::MeterProvider as _;
use opentelemetry_sdk::metrics::{InMemoryMetricExporter, PeriodicReader, SdkMeterProvider};

use super::*;

pub(crate) fn meter() -> (SdkMeterProvider, InMemoryMetricExporter) {
    let exporter = InMemoryMetricExporter::default();
    let provider = SdkMeterProvider::builder()
        .with_reader(PeriodicReader::builder(exporter.clone()).build())
        .build();
    (provider, exporter)
}

/// `(name, state) → value` of the i64 sums of the latest export.
fn sums(
    provider: &SdkMeterProvider,
    exporter: &InMemoryMetricExporter,
) -> std::collections::BTreeMap<(String, Option<String>), i64> {
    use opentelemetry_sdk::metrics::data::{AggregatedMetrics, MetricData};
    provider.force_flush().expect("flush");
    let metrics = exporter
        .get_finished_metrics()
        .expect("metrics")
        .pop()
        .expect("export");
    let mut values = std::collections::BTreeMap::new();
    for metric in metrics.scope_metrics().flat_map(|scope| scope.metrics()) {
        if let AggregatedMetrics::I64(MetricData::Sum(sum)) = metric.data() {
            for point in sum.data_points() {
                assert!(
                    point
                        .attributes()
                        .any(|kv| kv.key.as_str() == POOL_NAME_ATTRIBUTE
                            && kv.value.as_str() == POOL_NAME),
                    "{} carries the pool name",
                    metric.name()
                );
                let state = point
                    .attributes()
                    .find(|kv| kv.key.as_str() == STATE_ATTRIBUTE)
                    .map(|kv| kv.value.as_str().into_owned());
                values.insert((metric.name().to_owned(), state), point.value());
            }
        }
    }
    values
}

fn config() -> DatabaseConfig {
    DatabaseConfig {
        url: "postgres://u:p@127.0.0.1:1/db".to_owned(),
        max_conns: 3,
        min_conns: 1,
        acquire_timeout: Duration::from_millis(200),
        ..DatabaseConfig::default()
    }
}

#[tokio::test]
async fn options_carry_the_configured_limits() {
    let config = config();
    let options = pool_options(&config, config.max_conns);
    assert_eq!(options.get_max_connections(), 3);
    assert_eq!(options.get_min_connections(), 1);
    assert_eq!(options.get_acquire_timeout(), Duration::from_millis(200));
    assert_eq!(options.get_max_lifetime(), Some(config.max_lifetime));
    assert_eq!(options.get_idle_timeout(), Some(config.idle_timeout));
    // A smaller pool (startup migrations) never keeps more idle than it holds.
    assert_eq!(pool_options(&config, 1).get_min_connections(), 1);
    let none = DatabaseConfig {
        min_conns: 0,
        ..config
    };
    assert_eq!(pool_options(&none, 1).get_min_connections(), 0);
}

#[tokio::test]
async fn gauges_report_count_max_and_idle_min_of_the_pool() {
    let (provider, exporter) = meter();
    let config = config();
    let pool = Arc::new(
        pool_options(&config, config.max_conns)
            .min_connections(0)
            .connect_lazy(&config.url)
            .expect("lazy pool"),
    );
    let _metrics = PoolMetrics::new(&provider.meter("test"));
    _metrics.observe(&pool);

    let values = sums(&provider, &exporter);
    let get = |name: &str, state: Option<&str>| {
        values
            .get(&(name.to_owned(), state.map(str::to_owned)))
            .copied()
    };
    assert_eq!(get("db.client.connection.max", None), Some(3));
    assert_eq!(get("db.client.connection.idle.min", None), Some(0));
    assert_eq!(get("db.client.connection.count", Some("idle")), Some(0));
    assert_eq!(get("db.client.connection.count", Some("used")), Some(0));

    // A dropped pool is no longer observed.
    drop(pool);
    let (provider, exporter) = meter();
    let pool = Arc::new(
        pool_options(&config, 2)
            .min_connections(0)
            .connect_lazy(&config.url)
            .expect("lazy pool"),
    );
    let metrics = PoolMetrics::new(&provider.meter("test"));
    metrics.observe(&pool);
    drop(pool);
    drop(metrics);
    provider.force_flush().expect("flush");
    assert!(
        exporter
            .get_finished_metrics()
            .expect("metrics")
            .iter()
            .flat_map(|metrics| metrics.scope_metrics())
            .flat_map(|scope| scope.metrics())
            .all(|metric| metric.name() != "db.client.connection.max")
    );
}

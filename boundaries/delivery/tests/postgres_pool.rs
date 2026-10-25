//! Pool limits and `db.client.connection.*` metrics against a real
//! PostgreSQL. Run only with `DATABASE_URL`:
//!
//! `DATABASE_URL=postgres://postgres:postgres@localhost:5432/postgres cargo test --test postgres_pool`

use std::collections::BTreeMap;
use std::sync::Arc;
use std::time::Duration;

use opentelemetry::metrics::MeterProvider as _;
use opentelemetry_sdk::metrics::data::{AggregatedMetrics, MetricData};
use opentelemetry_sdk::metrics::{InMemoryMetricExporter, PeriodicReader, SdkMeterProvider};

use delivery::infrastructure::postgres::{PoolMetrics, pool_options};
use delivery::pkg::config::DatabaseConfig;

fn config() -> Option<DatabaseConfig> {
    let url = std::env::var("DATABASE_URL")
        .ok()
        .filter(|v| !v.is_empty())?;
    Some(DatabaseConfig {
        url,
        max_conns: 1,
        acquire_timeout: Duration::from_millis(300),
        ..DatabaseConfig::default()
    })
}

#[derive(Debug, Default)]
struct Exported {
    /// `(name, state)` → value of i64 sums.
    sums: BTreeMap<(String, Option<String>), i64>,
    counters: BTreeMap<String, u64>,
    /// name → recorded count of histograms.
    histograms: BTreeMap<String, u64>,
}

fn export(provider: &SdkMeterProvider, exporter: &InMemoryMetricExporter) -> Exported {
    provider.force_flush().expect("flush");
    let metrics = exporter
        .get_finished_metrics()
        .expect("metrics")
        .pop()
        .expect("export");
    let mut exported = Exported::default();
    for metric in metrics.scope_metrics().flat_map(|scope| scope.metrics()) {
        let name = metric.name().to_owned();
        match metric.data() {
            AggregatedMetrics::I64(MetricData::Sum(sum)) => {
                for point in sum.data_points() {
                    let state = point
                        .attributes()
                        .find(|kv| kv.key.as_str() == "db.client.connection.state")
                        .map(|kv| kv.value.as_str().into_owned());
                    exported.sums.insert((name.clone(), state), point.value());
                }
            }
            AggregatedMetrics::U64(MetricData::Sum(sum)) => {
                exported
                    .counters
                    .insert(name, sum.data_points().map(|point| point.value()).sum());
            }
            AggregatedMetrics::F64(MetricData::Histogram(histogram)) => {
                exported.histograms.insert(
                    name,
                    histogram.data_points().map(|point| point.count()).sum(),
                );
            }
            _ => {}
        }
    }
    exported
}

/// A saturated pool times out acquires after `acquire_timeout` and counts
/// the timeout; gauges show the used connection; wait and use times record.
#[tokio::test(flavor = "multi_thread")]
async fn saturated_pool_times_out_and_is_observed() {
    let Some(config) = config() else { return };
    let exporter = InMemoryMetricExporter::default();
    let provider = SdkMeterProvider::builder()
        .with_reader(PeriodicReader::builder(exporter.clone()).build())
        .build();
    let pool = Arc::new(
        pool_options(&config, config.max_conns)
            .connect(&config.url)
            .await
            .expect("pool"),
    );
    let metrics = PoolMetrics::new(&provider.meter("test"));
    metrics.observe(&pool);

    let held = metrics.acquire(&pool).await.expect("first connection");
    let started = std::time::Instant::now();
    let error = metrics
        .acquire(&pool)
        .await
        .err()
        .expect("pool of one is saturated");
    assert!(matches!(error, sqlx::Error::PoolTimedOut), "{error}");
    assert!(started.elapsed() >= config.acquire_timeout);

    let exported = export(&provider, &exporter);
    let sum = |name: &str, state: Option<&str>| {
        exported
            .sums
            .get(&(name.to_owned(), state.map(str::to_owned)))
            .copied()
    };
    assert_eq!(sum("db.client.connection.count", Some("used")), Some(1));
    assert_eq!(sum("db.client.connection.count", Some("idle")), Some(0));
    assert_eq!(sum("db.client.connection.max", None), Some(1));
    assert_eq!(sum("db.client.connection.pending_requests", None), Some(0));
    assert_eq!(
        exported.counters.get("db.client.connection.timeouts"),
        Some(&1)
    );
    assert_eq!(
        exported.histograms.get("db.client.connection.wait_time"),
        Some(&1)
    );

    drop(held);
    // The return to the pool runs on a spawned task.
    tokio::time::sleep(Duration::from_millis(200)).await;
    let exported = export(&provider, &exporter);
    assert_eq!(
        exported.histograms.get("db.client.connection.use_time"),
        Some(&1)
    );
    assert_eq!(
        exported.sums.get(&(
            "db.client.connection.count".to_owned(),
            Some("idle".to_owned())
        )),
        Some(&1)
    );
    pool.close().await;
}

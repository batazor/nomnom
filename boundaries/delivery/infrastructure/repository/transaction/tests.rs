use std::collections::BTreeMap;
use std::panic::{AssertUnwindSafe, catch_unwind};

use opentelemetry::metrics::MeterProvider as _;
use opentelemetry_sdk::metrics::data::{AggregatedMetrics, MetricData};
use opentelemetry_sdk::metrics::{InMemoryMetricExporter, PeriodicReader, SdkMeterProvider};

use super::*;
use crate::infrastructure::repository::storage_error;
use crate::infrastructure::repository::test_support::fresh_database;
use crate::pkg::persistence::RepositoryError;

struct Harness {
    provider: SdkMeterProvider,
    exporter: InMemoryMetricExporter,
    store: Store,
}

fn harness() -> Harness {
    let exporter = InMemoryMetricExporter::default();
    let provider = SdkMeterProvider::builder()
        .with_reader(PeriodicReader::builder(exporter.clone()).build())
        .build();
    let store = Store::connect(&fresh_database())
        .expect("connect test database")
        .with_transaction_metrics(TransactionMetrics::new(&provider.meter("test")));
    Harness {
        provider,
        exporter,
        store,
    }
}

#[derive(Debug, PartialEq)]
enum TestError {
    Work,
    Store(RepositoryError),
    UnitOfWork(UnitOfWorkError),
}

impl From<UnitOfWorkError> for TestError {
    fn from(error: UnitOfWorkError) -> Self {
        Self::UnitOfWork(error)
    }
}

/// One statement on the connection of the running transaction.
fn statement(store: &Store, statement: &'static str) -> Result<(), RepositoryError> {
    sql!(store, "CREATE", |connection| sqlx::raw_sql(statement)
        .execute(connection))
    .map(drop)
}

fn execute(store: &Store, sql: &'static str) -> Result<(), TestError> {
    statement(store, sql).map_err(TestError::Store)
}

impl Harness {
    /// `nomnom.outcome → (transactions, duration count)` of the latest export.
    fn outcomes(&self) -> BTreeMap<String, (u64, u64)> {
        self.provider.force_flush().expect("flush");
        let mut outcomes = BTreeMap::new();
        let Some(exported) = self.exporter.get_finished_metrics().expect("metrics").pop() else {
            return outcomes;
        };
        for metric in exported.scope_metrics().flat_map(|scope| scope.metrics()) {
            match (metric.name(), metric.data()) {
                ("nomnom.uow.transactions", AggregatedMetrics::U64(MetricData::Sum(sum))) => {
                    assert_eq!(metric.unit(), "{transaction}");
                    for point in sum.data_points() {
                        let entry: &mut (u64, u64) =
                            outcomes.entry(outcome(point.attributes())).or_default();
                        entry.0 = point.value();
                    }
                }
                (
                    "nomnom.uow.duration",
                    AggregatedMetrics::F64(MetricData::Histogram(histogram)),
                ) => {
                    assert_eq!(metric.unit(), "s");
                    for point in histogram.data_points() {
                        assert_eq!(point.bounds().collect::<Vec<_>>(), SECONDS_BUCKETS.to_vec());
                        let entry: &mut (u64, u64) =
                            outcomes.entry(outcome(point.attributes())).or_default();
                        entry.1 = point.count();
                    }
                }
                (name, _) => panic!("unexpected metric {name}"),
            }
        }
        outcomes
    }
}

fn outcome<'a>(mut attributes: impl Iterator<Item = &'a KeyValue>) -> String {
    attributes
        .find(|kv| kv.key.as_str() == "nomnom.outcome")
        .map(|kv| kv.value.as_str().into_owned())
        .expect("nomnom.outcome")
}

fn outcomes(expected: &[(&str, u64)]) -> BTreeMap<String, (u64, u64)> {
    expected
        .iter()
        .map(|(outcome, count)| ((*outcome).to_owned(), (*count, *count)))
        .collect()
}

#[test]
fn committed_and_rolled_back_transactions_are_counted_with_duration() {
    let harness = harness();

    let committed: Result<u8, TestError> = harness.store.run(|| {
        execute(&harness.store, "SELECT 1")?;
        Ok(7)
    });
    let rolled_back: Result<u8, TestError> = harness.store.run(|| Err(TestError::Work));

    assert_eq!((committed, rolled_back), (Ok(7), Err(TestError::Work)));
    assert_eq!(
        harness.outcomes(),
        outcomes(&[("committed", 1), ("rolled_back", 1)])
    );
}

#[test]
fn rejected_commit_is_a_commit_failure() {
    let harness = harness();

    // A deferred unique constraint is checked only at COMMIT.
    let result: Result<(), TestError> = harness.store.run(|| {
        execute(
            &harness.store,
            "CREATE TEMPORARY TABLE uow_deferred (
                 id integer,
                 CONSTRAINT uow_deferred_id UNIQUE (id) DEFERRABLE INITIALLY DEFERRED
             ) ON COMMIT DROP;
             INSERT INTO uow_deferred VALUES (1), (1)",
        )
    });

    assert!(
        matches!(
            result,
            Err(TestError::UnitOfWork(UnitOfWorkError::CommitFailed(_)))
        ),
        "{result:?}"
    );
    assert_eq!(harness.outcomes(), outcomes(&[("commit_failed", 1)]));
}

#[test]
fn panic_in_the_work_is_a_rollback_and_nested_run_records_nothing() {
    let harness = harness();

    let panicked = catch_unwind(AssertUnwindSafe(|| {
        let _: Result<(), TestError> = harness.store.run(|| panic!("work panicked"));
    }));
    let nested: Result<Result<(), TestError>, TestError> =
        harness.store.run(|| Ok(harness.store.run(|| Ok(()))));

    assert!(panicked.is_err());
    assert!(
        matches!(
            nested,
            Ok(Err(TestError::UnitOfWork(UnitOfWorkError::CommitFailed(_))))
        ),
        "{nested:?}"
    );
    assert_eq!(
        harness.outcomes(),
        outcomes(&[("committed", 1), ("rolled_back", 1)])
    );
}

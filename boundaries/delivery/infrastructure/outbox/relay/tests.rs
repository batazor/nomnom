use std::collections::HashSet;
use std::sync::Mutex;

use super::*;

fn entry(seq: i64) -> OutboxEntry {
    OutboxEntry {
        seq,
        event_id: format!("event-{seq}"),
        event_type: "nomnom.delivery.v1.DeliveryPickedUp".to_owned(),
        aggregate_type: "delivery".to_owned(),
        aggregate_id: "delivery-1".to_owned(),
        aggregate_version: seq,
        occurred_at: "2026-10-25T10:00:00.000000".to_owned(),
        occurred_at_epoch: now_epoch(),
        topic: OUTBOX_TOPIC.to_owned(),
        payload: Vec::new(),
        traceparent: None,
    }
}

/// `(row, published, attempts, last_error)`.
type FakeRow = (OutboxEntry, bool, u32, Option<String>);

#[derive(Default)]
struct FakeStore {
    rows: Mutex<Vec<FakeRow>>,
    /// The lock session survives this many checks; `None` — always.
    lock_lost_after: Option<usize>,
    lock_checks: Mutex<usize>,
}

impl FakeStore {
    fn with(seqs: &[i64]) -> Self {
        Self {
            rows: Mutex::new(
                seqs.iter()
                    .map(|seq| (entry(*seq), false, 0, None))
                    .collect(),
            ),
            ..Self::default()
        }
    }

    fn published(&self) -> Vec<i64> {
        let rows = self.rows.lock().expect("rows");
        rows.iter()
            .filter(|row| row.1)
            .map(|row| row.0.seq)
            .collect()
    }

    fn failure(&self, seq: i64) -> (u32, Option<String>) {
        let rows = self.rows.lock().expect("rows");
        let row = rows.iter().find(|row| row.0.seq == seq).expect("row");
        (row.2, row.3.clone())
    }
}

impl OutboxStore for FakeStore {
    async fn holds_lock(&self) -> bool {
        let mut checks = self.lock_checks.lock().expect("lock checks");
        *checks += 1;
        self.lock_lost_after.is_none_or(|alive| *checks <= alive)
    }

    async fn fetch(&self, limit: u32) -> Result<Vec<OutboxEntry>, String> {
        let rows = self.rows.lock().expect("rows");
        Ok(rows
            .iter()
            .filter(|row| !row.1)
            .take(limit as usize)
            .map(|row| row.0.clone())
            .collect())
    }

    async fn mark_published(&self, seq: i64) -> Result<(), String> {
        let mut rows = self.rows.lock().expect("rows");
        let row = rows.iter_mut().find(|row| row.0.seq == seq).expect("row");
        row.1 = true;
        Ok(())
    }

    async fn record_failure(&self, seq: i64, error: &str) -> Result<(), String> {
        let mut rows = self.rows.lock().expect("rows");
        let row = rows.iter_mut().find(|row| row.0.seq == seq).expect("row");
        row.2 += 1;
        row.3 = Some(error.to_owned());
        Ok(())
    }
}

/// Fails the listed `seq`s; records every attempt.
#[derive(Default)]
struct FakePublisher {
    failing: Mutex<HashSet<i64>>,
    attempts: Mutex<Vec<i64>>,
}

impl FakePublisher {
    fn failing(seqs: &[i64]) -> Self {
        Self {
            failing: Mutex::new(seqs.iter().copied().collect()),
            attempts: Mutex::default(),
        }
    }

    fn heal(&self) {
        self.failing.lock().expect("failing").clear();
    }

    fn attempts(&self) -> Vec<i64> {
        self.attempts.lock().expect("attempts").clone()
    }
}

impl Publisher for FakePublisher {
    async fn publish(&self, entry: &OutboxEntry) -> Result<(), PublishError> {
        self.attempts.lock().expect("attempts").push(entry.seq);
        if self.failing.lock().expect("failing").contains(&entry.seq) {
            return Err(PublishError {
                class: "timeout",
                message: "Message timed out".to_owned(),
            });
        }
        Ok(())
    }
}

fn metrics() -> RelayMetrics {
    RelayMetrics::global()
}

#[tokio::test]
async fn publishes_batch_in_seq_order() {
    let store = FakeStore::with(&[1, 2, 3]);
    let publisher = FakePublisher::default();
    let outcome = relay_batch(&store, &publisher, 10, &metrics())
        .await
        .expect("batch");
    assert_eq!(
        outcome,
        BatchOutcome {
            fetched: 3,
            published: 3,
            failed: None,
            lock_lost: false,
        }
    );
    assert_eq!(publisher.attempts(), vec![1, 2, 3]);
    assert_eq!(store.published(), vec![1, 2, 3]);
}

#[tokio::test]
async fn first_failure_stops_the_batch() {
    let store = FakeStore::with(&[1, 2, 3, 4]);
    let publisher = FakePublisher::failing(&[2]);
    let outcome = relay_batch(&store, &publisher, 10, &metrics())
        .await
        .expect("batch");
    assert_eq!(
        outcome,
        BatchOutcome {
            fetched: 4,
            published: 1,
            failed: Some(2),
            lock_lost: false,
        }
    );
    // Rows after the failed one are not attempted in this cycle.
    assert_eq!(publisher.attempts(), vec![1, 2]);
    assert_eq!(store.published(), vec![1]);
    assert_eq!(store.failure(2), (1, Some("Message timed out".to_owned())));
    assert_eq!(store.failure(3), (0, None));
}

#[tokio::test]
async fn next_cycle_starts_from_the_failed_row() {
    let store = FakeStore::with(&[1, 2, 3]);
    let publisher = FakePublisher::failing(&[1]);
    for _ in 0..2 {
        let outcome = relay_batch(&store, &publisher, 10, &metrics())
            .await
            .expect("batch");
        assert_eq!(outcome.failed, Some(1));
    }
    assert_eq!(store.failure(1).0, 2);
    assert!(store.published().is_empty());

    publisher.heal();
    let outcome = relay_batch(&store, &publisher, 10, &metrics())
        .await
        .expect("batch");
    assert_eq!(outcome.published, 3);
    assert_eq!(publisher.attempts(), vec![1, 1, 1, 2, 3]);
    assert_eq!(store.published(), vec![1, 2, 3]);
}

#[tokio::test]
async fn batch_size_limits_one_batch() {
    let store = FakeStore::with(&[1, 2, 3]);
    let publisher = FakePublisher::default();
    let outcome = relay_batch(&store, &publisher, 2, &metrics())
        .await
        .expect("batch");
    assert_eq!(outcome.fetched, 2);
    assert_eq!(store.published(), vec![1, 2]);
}

/// FR-537: the lock session is checked before every record; once it is lost
/// the batch stops before the next record, which a standby may publish.
#[tokio::test]
async fn lost_lock_stops_the_batch_before_the_next_record() {
    let store = FakeStore {
        lock_lost_after: Some(2),
        ..FakeStore::with(&[1, 2, 3, 4])
    };
    let publisher = FakePublisher::default();
    let outcome = relay_batch(&store, &publisher, 10, &metrics())
        .await
        .expect("batch");
    assert_eq!(
        outcome,
        BatchOutcome {
            fetched: 4,
            published: 2,
            failed: None,
            lock_lost: true,
        }
    );
    assert_eq!(publisher.attempts(), vec![1, 2]);
    assert_eq!(store.published(), vec![1, 2]);
    assert_eq!(*store.lock_checks.lock().expect("lock checks"), 3);
}

#[tokio::test]
async fn relay_without_the_lock_publishes_nothing() {
    let store = FakeStore {
        lock_lost_after: Some(0),
        ..FakeStore::with(&[1, 2])
    };
    let publisher = FakePublisher::default();
    let outcome = relay_batch(&store, &publisher, 10, &metrics())
        .await
        .expect("batch");
    assert!(outcome.lock_lost);
    assert!(publisher.attempts().is_empty());
    assert!(store.published().is_empty());
}

mod backlog {
    use opentelemetry::metrics::MeterProvider as _;
    use opentelemetry_sdk::metrics::data::{AggregatedMetrics, MetricData};
    use opentelemetry_sdk::metrics::{InMemoryMetricExporter, PeriodicReader, SdkMeterProvider};

    use super::*;

    struct Gauges {
        provider: SdkMeterProvider,
        exporter: InMemoryMetricExporter,
        metrics: RelayMetrics,
    }

    impl Gauges {
        fn new() -> Self {
            let exporter = InMemoryMetricExporter::default();
            let provider = SdkMeterProvider::builder()
                .with_reader(PeriodicReader::builder(exporter.clone()).build())
                .build();
            let metrics = RelayMetrics::new(&provider.meter("test"));
            Self {
                provider,
                exporter,
                metrics,
            }
        }

        /// `stream → (pending, oldest age)` of the latest export.
        fn exported(&self) -> std::collections::BTreeMap<String, (u64, f64)> {
            self.provider.force_flush().expect("flush");
            let metrics = self
                .exporter
                .get_finished_metrics()
                .expect("metrics")
                .pop()
                .expect("an export");
            let mut gauges = std::collections::BTreeMap::<String, (u64, f64)>::new();
            for metric in metrics.scope_metrics().flat_map(|scope| scope.metrics()) {
                match (metric.name(), metric.data()) {
                    (
                        "nomnom.outbox.pending.messages",
                        AggregatedMetrics::U64(MetricData::Gauge(gauge)),
                    ) => {
                        for point in gauge.data_points() {
                            let stream = stream_of(point.attributes());
                            gauges.entry(stream).or_default().0 = point.value();
                        }
                    }
                    (
                        "nomnom.outbox.oldest_pending.age",
                        AggregatedMetrics::F64(MetricData::Gauge(gauge)),
                    ) => {
                        for point in gauge.data_points() {
                            let stream = stream_of(point.attributes());
                            gauges.entry(stream).or_default().1 = point.value();
                        }
                    }
                    _ => {}
                }
            }
            gauges
        }
    }

    fn stream_of<'a>(mut attributes: impl Iterator<Item = &'a opentelemetry::KeyValue>) -> String {
        attributes
            .find(|kv| kv.key.as_str() == "messaging.destination.name")
            .map(|kv| kv.value.as_str().into_owned())
            .expect("stream attribute")
    }

    fn row(stream: &str, pending: u64, age: f64) -> (String, u64, f64) {
        (stream.to_owned(), pending, age)
    }

    /// The Outbox topic reports 0 before it ever had a backlog.
    #[test]
    fn empty_outbox_reports_zero_for_the_outbox_topic() {
        let gauges = Gauges::new();
        let mut streams = BTreeSet::new();
        report_backlog(&gauges.metrics, &mut streams, &[]);
        assert_eq!(
            gauges.exported(),
            [(OUTBOX_TOPIC.to_owned(), (0, 0.0))].into()
        );
    }

    /// A stream whose backlog emptied reports 0 instead of keeping (or
    /// dropping) its last value.
    #[test]
    fn emptied_stream_reports_zero() {
        let gauges = Gauges::new();
        let mut streams = BTreeSet::new();
        report_backlog(
            &gauges.metrics,
            &mut streams,
            &[
                row(OUTBOX_TOPIC, 3, 12.5),
                row("delivery.v0.legacy", 2, 40.0),
            ],
        );
        assert_eq!(
            gauges.exported(),
            [
                ("delivery.v0.legacy".to_owned(), (2, 40.0)),
                (OUTBOX_TOPIC.to_owned(), (3, 12.5)),
            ]
            .into()
        );

        report_backlog(&gauges.metrics, &mut streams, &[row(OUTBOX_TOPIC, 1, 2.0)]);
        assert_eq!(
            gauges.exported(),
            [
                ("delivery.v0.legacy".to_owned(), (0, 0.0)),
                (OUTBOX_TOPIC.to_owned(), (1, 2.0)),
            ]
            .into()
        );

        report_backlog(&gauges.metrics, &mut streams, &[]);
        assert_eq!(
            gauges.exported(),
            [
                ("delivery.v0.legacy".to_owned(), (0, 0.0)),
                (OUTBOX_TOPIC.to_owned(), (0, 0.0)),
            ]
            .into()
        );
    }
}

//! Relay cycle: head-of-line publication of one batch and the
//! [`RelayWorker`] that runs cycles under the advisory lock.

use std::future::Future;
use std::io;
use std::sync::Arc;
use std::time::{Duration, Instant, SystemTime, UNIX_EPOCH};

use tokio::sync::watch;

use super::metrics::RelayMetrics;
use super::postgres::PgOutbox;
use crate::infrastructure::postgres::OUTBOX_TOPIC;
use crate::pkg::config::OutboxConfig;
use crate::pkg::lifecycle::{Worker, wait_for_stop};

/// Unpublished Outbox row as the relay reads it.
#[derive(Clone, Debug, PartialEq)]
pub struct OutboxEntry {
    pub seq: i64,
    pub event_id: String,
    pub event_type: String,
    pub aggregate_type: String,
    pub aggregate_id: String,
    pub aggregate_version: i64,
    /// UTC `YYYY-MM-DDTHH:MM:SS.ffffff`.
    pub occurred_at: String,
    /// `occurred_at` as Unix seconds, for the lag metric.
    pub occurred_at_epoch: f64,
    pub topic: String,
    pub payload: Vec<u8>,
    pub traceparent: Option<String>,
}

/// Failed publication of one row.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct PublishError {
    /// Closed set for the `error_class` label: `timeout`, `queue_full`,
    /// `broker`, `client`.
    pub class: &'static str,
    pub message: String,
}

/// Publishes one row and resolves after the broker acknowledged it.
pub trait Publisher: Send + Sync + 'static {
    fn publish(&self, entry: &OutboxEntry)
    -> impl Future<Output = Result<(), PublishError>> + Send;
}

impl<P: Publisher> Publisher for Arc<P> {
    fn publish(
        &self,
        entry: &OutboxEntry,
    ) -> impl Future<Output = Result<(), PublishError>> + Send {
        P::publish(self, entry)
    }
}

/// Outbox operations of one cycle.
pub trait OutboxStore: Send + Sync + 'static {
    /// Up to `limit` unpublished rows in `seq` order.
    fn fetch(&self, limit: u32) -> impl Future<Output = Result<Vec<OutboxEntry>, String>> + Send;
    fn mark_published(&self, seq: i64) -> impl Future<Output = Result<(), String>> + Send;
    fn record_failure(
        &self,
        seq: i64,
        error: &str,
    ) -> impl Future<Output = Result<(), String>> + Send;
}

/// Result of one batch.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct BatchOutcome {
    pub fetched: usize,
    pub published: usize,
    /// `seq` of the row that stopped the batch.
    pub failed: Option<i64>,
}

fn now_epoch() -> f64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map_or(0.0, |elapsed| elapsed.as_secs_f64())
}

/// Publishes one batch in `seq` order. Each acknowledged row is marked
/// published at once; the first failure records `attempts`/`last_error` and
/// stops the batch, so later rows wait for the next cycle (head-of-line).
pub async fn relay_batch<S: OutboxStore, P: Publisher>(
    store: &S,
    publisher: &P,
    batch_size: u32,
    metrics: &RelayMetrics,
) -> Result<BatchOutcome, String> {
    let entries = store.fetch(batch_size).await?;
    let mut outcome = BatchOutcome {
        fetched: entries.len(),
        ..BatchOutcome::default()
    };
    for entry in &entries {
        let started = Instant::now();
        match publisher.publish(entry).await {
            Ok(()) => {
                metrics.published(
                    &entry.topic,
                    &entry.event_type,
                    started.elapsed(),
                    now_epoch() - entry.occurred_at_epoch,
                );
                store.mark_published(entry.seq).await?;
                outcome.published += 1;
                println!(
                    "outbox published event_id={} event_type={} seq={}",
                    entry.event_id, entry.event_type, entry.seq
                );
            }
            Err(error) => {
                metrics.failed(
                    &entry.topic,
                    &entry.event_type,
                    error.class,
                    started.elapsed(),
                );
                eprintln!(
                    "outbox publish failed event_id={} event_type={} seq={} error_class={}: {}",
                    entry.event_id, entry.event_type, entry.seq, error.class, error.message
                );
                store.record_failure(entry.seq, &error.message).await?;
                outcome.failed = Some(entry.seq);
                break;
            }
        }
    }
    Ok(outcome)
}

/// [`Worker`] of `delivery-relay`: polls the Outbox while it holds the
/// advisory lock, otherwise stays a standby replica.
pub struct RelayWorker<P> {
    outbox: Arc<PgOutbox>,
    publisher: P,
    config: OutboxConfig,
    metrics: RelayMetrics,
}

impl<P: Publisher> RelayWorker<P> {
    #[must_use]
    pub fn new(
        outbox: Arc<PgOutbox>,
        publisher: P,
        config: OutboxConfig,
        metrics: RelayMetrics,
    ) -> Self {
        Self {
            outbox,
            publisher,
            config,
            metrics,
        }
    }

    /// Batches while they come back full and clean, then the backlog gauges.
    async fn cycle(&self, stop: &watch::Receiver<bool>) {
        loop {
            match relay_batch(
                self.outbox.as_ref(),
                &self.publisher,
                self.config.batch_size,
                &self.metrics,
            )
            .await
            {
                Ok(outcome)
                    if outcome.failed.is_none()
                        && outcome.fetched == self.config.batch_size as usize
                        && !*stop.borrow() => {}
                Ok(_) => break,
                Err(error) => {
                    eprintln!("outbox relay cycle: {error}");
                    break;
                }
            }
        }
        match self.outbox.backlog().await {
            Ok(backlog) => {
                if !backlog.iter().any(|(stream, _, _)| stream == OUTBOX_TOPIC) {
                    self.metrics.backlog(OUTBOX_TOPIC, 0, 0.0);
                }
                for (stream, pending, age) in backlog {
                    self.metrics.backlog(&stream, pending, age);
                }
            }
            Err(error) => eprintln!("outbox backlog: {error}"),
        }
    }

    async fn serve(&self, mut stop: watch::Receiver<bool>) {
        let mut holding = false;
        let mut next_cleanup = Instant::now();
        while !*stop.borrow() {
            match self.outbox.try_acquire_lock().await {
                Ok(acquired) => {
                    if acquired != holding {
                        println!(
                            "outbox relay {}",
                            if acquired {
                                "is active: lock acquired"
                            } else {
                                "is standby: lock lost"
                            }
                        );
                    }
                    holding = acquired;
                }
                Err(error) => {
                    eprintln!("outbox lock: {error}");
                    holding = false;
                }
            }
            if holding {
                self.cycle(&stop).await;
                if Instant::now() >= next_cleanup {
                    match self.outbox.cleanup(self.config.retention).await {
                        Ok(0) => {}
                        Ok(deleted) => println!("outbox retention deleted {deleted} rows"),
                        Err(error) => eprintln!("outbox retention: {error}"),
                    }
                    next_cleanup = Instant::now() + self.config.cleanup_interval;
                }
            }
            tokio::select! {
                () = tokio::time::sleep(self.config.poll_interval) => {}
                () = wait_for_stop(&mut stop) => {}
            }
        }
    }
}

/// Drain finishes the row in flight; the lock is released explicitly.
const RELEASE_TIMEOUT: Duration = Duration::from_secs(5);

impl<P: Publisher> Worker for RelayWorker<P> {
    async fn run(self, stop: watch::Receiver<bool>) -> io::Result<()> {
        self.serve(stop).await;
        if tokio::time::timeout(RELEASE_TIMEOUT, self.outbox.release_lock())
            .await
            .is_err()
        {
            eprintln!("outbox lock release timed out");
        }
        Ok(())
    }
}

#[cfg(test)]
mod tests {
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
    }

    impl FakeStore {
        fn with(seqs: &[i64]) -> Self {
            Self {
                rows: Mutex::new(
                    seqs.iter()
                        .map(|seq| (entry(*seq), false, 0, None))
                        .collect(),
                ),
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
                failed: None
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
                failed: Some(2)
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
}

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

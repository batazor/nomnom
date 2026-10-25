//! Relay cycle: head-of-line publication of one batch and the
//! [`RelayWorker`] that runs cycles under the advisory lock.

use std::collections::BTreeSet;
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
use crate::pkg::telemetry::messaging::{self, Publication};

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
    /// `true` while the session of the exclusivity lock is alive; checked
    /// before every record (FR-537).
    fn holds_lock(&self) -> impl Future<Output = bool> + Send;
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
    /// The lock session was lost before a record: the batch stopped there.
    pub lock_lost: bool,
}

fn now_epoch() -> f64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map_or(0.0, |elapsed| elapsed.as_secs_f64())
}

/// Publishes one batch in `seq` order. Each acknowledged row is marked
/// published at once; the first failure records `attempts`/`last_error` and
/// stops the batch, so later rows wait for the next cycle (head-of-line).
/// The lock session is checked before every record: once it is lost, a
/// standby may already publish, so the batch stops before the next record
/// (FR-537).
pub async fn relay_batch<S: OutboxStore, P: Publisher>(
    store: &S,
    publisher: &P,
    batch_size: u32,
    metrics: &RelayMetrics,
) -> Result<BatchOutcome, String> {
    let entries = store.fetch(batch_size).await?;
    let tracer = opentelemetry::global::tracer(messaging::INSTRUMENTATION);
    let mut outcome = BatchOutcome {
        fetched: entries.len(),
        ..BatchOutcome::default()
    };
    for entry in &entries {
        if !store.holds_lock().await {
            tracing::warn!(
                operation = "outbox.relay",
                seq = entry.seq,
                "outbox relay lost the lock: batch stopped"
            );
            outcome.lock_lost = true;
            break;
        }
        let started = Instant::now();
        // Producer span continues the trace stored with the row; the Kafka
        // header carries the producer span.
        let span = messaging::start_send(
            &tracer,
            &Publication {
                topic: &entry.topic,
                event_id: &entry.event_id,
                event_type: &entry.event_type,
                traceparent: entry.traceparent.as_deref(),
            },
        );
        let sent = OutboxEntry {
            traceparent: messaging::traceparent_of(&span).or_else(|| entry.traceparent.clone()),
            ..entry.clone()
        };
        let published = publisher.publish(&sent).await;
        messaging::end_send(&span, published.as_ref().err().map(|error| error.class));
        match published {
            Ok(()) => {
                metrics.published(
                    &entry.topic,
                    &entry.event_type,
                    started.elapsed(),
                    now_epoch() - entry.occurred_at_epoch,
                );
                store.mark_published(entry.seq).await?;
                outcome.published += 1;
                tracing::info!(
                    operation = "outbox.publish",
                    outcome = "success",
                    event_id = %entry.event_id,
                    event_type = %entry.event_type,
                    seq = entry.seq,
                    "outbox event published"
                );
            }
            Err(error) => {
                metrics.failed(
                    &entry.topic,
                    &entry.event_type,
                    error.class,
                    started.elapsed(),
                );
                tracing::warn!(
                    operation = "outbox.publish",
                    outcome = "error",
                    event_id = %entry.event_id,
                    event_type = %entry.event_type,
                    seq = entry.seq,
                    error.type = error.class,
                    error.message = %error.message,
                    "outbox publish failed"
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
    /// Streams the backlog gauges have reported; see [`report_backlog`].
    streams: std::sync::Mutex<BTreeSet<String>>,
}

/// Records the backlog gauges of one cycle. `backlog` lists only streams with
/// unpublished rows, so every stream reported before (and the Outbox topic
/// from the start) that is absent now records `0` pending and `0` age instead
/// of keeping its last value.
pub fn report_backlog(
    metrics: &RelayMetrics,
    streams: &mut BTreeSet<String>,
    backlog: &[(String, u64, f64)],
) {
    streams.insert(OUTBOX_TOPIC.to_owned());
    for stream in streams.iter() {
        if !backlog.iter().any(|(pending, _, _)| pending == stream) {
            metrics.backlog(stream, 0, 0.0);
        }
    }
    for (stream, pending, age) in backlog {
        metrics.backlog(stream, *pending, *age);
        streams.insert(stream.clone());
    }
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
            streams: std::sync::Mutex::default(),
        }
    }

    /// Batches while they come back full and clean, then the backlog gauges.
    /// Returns `false` when the lock was lost during the cycle.
    async fn cycle(&self, stop: &watch::Receiver<bool>) -> bool {
        loop {
            match relay_batch(
                self.outbox.as_ref(),
                &self.publisher,
                self.config.batch_size,
                &self.metrics,
            )
            .await
            {
                Ok(outcome) if outcome.lock_lost => return false,
                Ok(outcome)
                    if outcome.failed.is_none()
                        && outcome.fetched == self.config.batch_size as usize
                        && !*stop.borrow() => {}
                Ok(_) => break,
                Err(error) => {
                    tracing::error!(operation = "outbox.relay", error.message = %error, "outbox relay cycle failed");
                    break;
                }
            }
        }
        match self.outbox.backlog().await {
            Ok(backlog) => {
                let mut streams = self
                    .streams
                    .lock()
                    .unwrap_or_else(std::sync::PoisonError::into_inner);
                report_backlog(&self.metrics, &mut streams, &backlog);
            }
            Err(error) => {
                tracing::warn!(operation = "outbox.backlog", error.message = %error, "outbox backlog query failed");
            }
        }
        true
    }

    async fn serve(&self, mut stop: watch::Receiver<bool>) {
        let mut holding = false;
        let mut next_cleanup = Instant::now();
        while !*stop.borrow() {
            match self.outbox.try_acquire_lock().await {
                Ok(acquired) => {
                    if acquired != holding {
                        tracing::info!(
                            operation = "outbox.lock",
                            active = acquired,
                            "{}",
                            if acquired {
                                "outbox relay is active: lock acquired"
                            } else {
                                "outbox relay is standby: lock lost"
                            }
                        );
                    }
                    holding = acquired;
                }
                Err(error) => {
                    tracing::warn!(operation = "outbox.lock", error.message = %error, "outbox lock failed");
                    holding = false;
                }
            }
            if holding {
                if !self.cycle(&stop).await {
                    // Standby until the lock is acquired again; no cleanup.
                    tracing::info!(
                        operation = "outbox.lock",
                        active = false,
                        "outbox relay is standby: lock lost"
                    );
                    holding = false;
                } else if Instant::now() >= next_cleanup {
                    match self.outbox.cleanup(self.config.retention).await {
                        Ok(0) => {}
                        Ok(deleted) => {
                            tracing::info!(
                                operation = "outbox.retention",
                                deleted,
                                "outbox retention deleted rows"
                            );
                        }
                        Err(error) => {
                            tracing::warn!(operation = "outbox.retention", error.message = %error, "outbox retention failed");
                        }
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
            tracing::warn!(operation = "outbox.lock", "outbox lock release timed out");
        }
        Ok(())
    }
}

#[cfg(test)]
mod tests;

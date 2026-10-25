//! Polling relay of the Transactional Outbox (ADR 13, ADR 31): the
//! `delivery-relay` process reads unpublished `delivery_outbox` rows in `seq`
//! order and publishes them to Kafka.
//!
//! - single active instance: session-level `pg_try_advisory_lock` on a
//!   dedicated connection; replicas without the lock do not read the Outbox;
//! - a row is marked `published_at` only after the broker acknowledged it
//!   (idempotent producer, `acks=all`); a repeated publication after a crash
//!   is expected (ADR 13), consumers deduplicate by `event_id`;
//! - head-of-line: the first failed row stops the batch, its `attempts` and
//!   `last_error` are updated and the next cycle starts from it;
//! - published rows older than `OUTBOX__RETENTION` are deleted.

mod envelope;
mod kafka;
mod metrics;
mod postgres;
mod relay;

pub use envelope::{EnvelopeHeaders, WATERMILL_UUID_HEADER, format_occurred_at, headers};
pub use kafka::KafkaPublisher;
pub use metrics::RelayMetrics;
pub use postgres::{
    LOCK_CHECK_TIMEOUT, PgOutbox, RELAY_LOCK_KEY, advisory_lock_parts, holds_advisory_lock,
    lock_connect_options,
};
pub use relay::{
    BatchOutcome, OutboxEntry, OutboxStore, PublishError, Publisher, RelayWorker, relay_batch,
    report_backlog,
};

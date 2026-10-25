use std::sync::atomic::{AtomicI64, AtomicU64, Ordering};

use crate::pkg::application::{Clock, IdGenerator};

/// Clock whose time is set explicitly by the test.
#[derive(Debug)]
pub struct ManualClock {
    now: AtomicI64,
}

impl ManualClock {
    #[must_use]
    pub const fn new(now_epoch_seconds: i64) -> Self {
        Self {
            now: AtomicI64::new(now_epoch_seconds),
        }
    }

    pub fn set(&self, now_epoch_seconds: i64) {
        self.now.store(now_epoch_seconds, Ordering::SeqCst);
    }

    pub fn advance(&self, seconds: i64) {
        self.now.fetch_add(seconds, Ordering::SeqCst);
    }
}

impl Clock for ManualClock {
    fn now_epoch_seconds(&self) -> i64 {
        self.now.load(Ordering::SeqCst)
    }
}

/// Deterministic identifiers `<prefix>-1`, `<prefix>-2`, ...
#[derive(Debug)]
pub struct SequentialIdGenerator {
    prefix: String,
    next: AtomicU64,
}

impl SequentialIdGenerator {
    #[must_use]
    pub fn new(prefix: impl Into<String>) -> Self {
        Self {
            prefix: prefix.into(),
            next: AtomicU64::new(1),
        }
    }
}

impl IdGenerator for SequentialIdGenerator {
    fn next_id(&self) -> String {
        let value = self.next.fetch_add(1, Ordering::SeqCst);
        format!("{}-{value}", self.prefix)
    }
}

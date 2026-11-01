use std::sync::atomic::{AtomicI64, Ordering};

use crate::slices::courier::applications::Clock as CourierClock;
use crate::slices::delivery::applications::Clock as DeliveryClock;

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

impl CourierClock for ManualClock {
    fn now_epoch_seconds(&self) -> i64 {
        self.now.load(Ordering::SeqCst)
    }
}

impl DeliveryClock for ManualClock {
    fn now_epoch_seconds(&self) -> i64 {
        self.now.load(Ordering::SeqCst)
    }
}

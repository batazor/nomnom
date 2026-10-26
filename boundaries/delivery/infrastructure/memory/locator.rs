use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Mutex, PoisonError};

use crate::slices::courier::domains::courier::{CourierId, MarketId};
use crate::slices::delivery::applications::courier_locator::{
    CourierCandidate, CourierLocator, LocatorError,
};

/// Courier locator returning preconfigured snapshots for every delivery.
#[derive(Debug, Default)]
pub struct StaticCourierLocator {
    candidates: Mutex<Vec<CourierCandidate>>,
    unavailable: AtomicBool,
}

impl StaticCourierLocator {
    #[must_use]
    pub fn new(candidates: Vec<CourierCandidate>) -> Self {
        Self {
            candidates: Mutex::new(candidates),
            unavailable: AtomicBool::new(false),
        }
    }

    pub fn set_candidates(&self, candidates: Vec<CourierCandidate>) {
        *self
            .candidates
            .lock()
            .unwrap_or_else(PoisonError::into_inner) = candidates;
    }

    pub fn set_unavailable(&self, unavailable: bool) {
        self.unavailable.store(unavailable, Ordering::SeqCst);
    }

    fn configured(&self) -> Result<Vec<CourierCandidate>, LocatorError> {
        if self.unavailable.load(Ordering::SeqCst) {
            return Err(LocatorError::Unavailable("locator offline".to_owned()));
        }
        Ok(self
            .candidates
            .lock()
            .unwrap_or_else(PoisonError::into_inner)
            .clone())
    }
}

impl CourierLocator for StaticCourierLocator {
    /// Every configured candidate, whatever its Market: tests prove that
    /// the assignment rule itself rejects couriers of another Market.
    fn candidates(&self, _market_id: &MarketId) -> Result<Vec<CourierCandidate>, LocatorError> {
        self.configured()
    }

    fn snapshot(&self, courier_id: &CourierId) -> Result<Option<CourierCandidate>, LocatorError> {
        Ok(self
            .configured()?
            .into_iter()
            .find(|candidate| &candidate.courier_id == courier_id))
    }
}

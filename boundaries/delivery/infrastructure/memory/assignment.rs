use std::sync::{Mutex, PoisonError};

use crate::slices::courier::applications::candidate_wake::{CandidateWake, CandidateWakeError};
use crate::slices::courier::domains::courier::CourierId;
use crate::slices::delivery::applications::assignment_process::{
    AssignmentProcess, AssignmentProcessError, WakeReason,
};
use crate::slices::delivery::domains::delivery::DeliveryId;

/// One call of the Assignment Workflow ports requested by a use case.
#[derive(Clone, Debug, Eq, PartialEq)]
pub enum AssignmentCall {
    Start(DeliveryId),
    Wake(DeliveryId, WakeReason),
    CourierAvailable(CourierId),
}

/// Test and local Assignment Workflow: records every call and can simulate a
/// Temporal outage. It starts no workflow, so it is not a production
/// implementation.
#[derive(Debug, Default)]
pub struct RecordingAssignment {
    calls: Mutex<Vec<AssignmentCall>>,
    failure: Mutex<Option<String>>,
}

impl RecordingAssignment {
    #[must_use]
    pub fn new() -> Self {
        Self::default()
    }

    /// Every call fails with `reason` until [`Self::set_failure`] clears it.
    pub fn set_failure(&self, reason: Option<&str>) {
        *self.failure.lock().unwrap_or_else(PoisonError::into_inner) = reason.map(str::to_owned);
    }

    /// Successful calls in call order.
    #[must_use]
    pub fn calls(&self) -> Vec<AssignmentCall> {
        self.calls
            .lock()
            .unwrap_or_else(PoisonError::into_inner)
            .clone()
    }

    fn record(&self, call: AssignmentCall) -> Result<(), String> {
        if let Some(reason) = self
            .failure
            .lock()
            .unwrap_or_else(PoisonError::into_inner)
            .clone()
        {
            return Err(reason);
        }
        self.calls
            .lock()
            .unwrap_or_else(PoisonError::into_inner)
            .push(call);
        Ok(())
    }
}

impl AssignmentProcess for RecordingAssignment {
    fn start(&self, delivery_id: &DeliveryId) -> Result<(), AssignmentProcessError> {
        self.record(AssignmentCall::Start(delivery_id.clone()))
            .map_err(AssignmentProcessError)
    }

    fn wake(
        &self,
        delivery_id: &DeliveryId,
        reason: WakeReason,
    ) -> Result<(), AssignmentProcessError> {
        self.record(AssignmentCall::Wake(delivery_id.clone(), reason))
            .map_err(AssignmentProcessError)
    }
}

impl CandidateWake for RecordingAssignment {
    fn courier_available(&self, courier_id: &CourierId) -> Result<(), CandidateWakeError> {
        self.record(AssignmentCall::CourierAvailable(courier_id.clone()))
            .map_err(CandidateWakeError)
    }
}

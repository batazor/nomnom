//! Port of the courier search: a courier who became a Courier Candidate —
//! available with a stored position — wakes the Assignment Workflow of one
//! waiting Delivery of its Market (FR-706). The `courier` slice does not know
//! `Delivery` (FR-486): a top-level adapter picks the Delivery and signals
//! its workflow. Called after commit, never inside a Unit of Work.

use std::fmt::{Display, Formatter};

use crate::slices::courier::domains::courier::CourierId;

/// The Delivery could not be picked or its workflow could not be signalled.
/// The commit already happened; the caller repeats the call.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct CandidateWakeError(pub String);

impl Display for CandidateWakeError {
    fn fmt(&self, formatter: &mut Formatter<'_>) -> std::fmt::Result {
        write!(formatter, "courier search wake failed: {}", self.0)
    }
}

pub trait CandidateWake {
    /// Wakes the oldest waiting Delivery of the courier's Market without a
    /// pending offer that was never offered to the courier; does nothing
    /// without such a Delivery.
    fn courier_available(&self, courier_id: &CourierId) -> Result<(), CandidateWakeError>;
}

//! Port of the Assignment Workflow (FR-704, FR-707): the durable process that
//! searches a courier for one Delivery. Use cases start it and wake it after
//! their commit, never inside a Unit of Work; the workflow reads the stored
//! state on every wake, so a wake carries no decision.

use std::fmt::{Display, Formatter};

use crate::pkg::persistence::RepositoryError;
use crate::slices::courier::domains::courier::CourierId;
use crate::slices::delivery::domains::delivery::DeliveryId;

/// Why a workflow is woken; only for diagnostics, the workflow re-reads the
/// state either way.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum WakeReason {
    /// A dispatcher offered the Delivery to a courier.
    OfferMade,
    /// The addressee accepted or rejected the pending offer.
    OfferAnswered,
    /// A courier became a Courier Candidate in the Market of the Delivery.
    CourierAvailable,
    /// The Courier Assignment ended before Pickup.
    CourierUnassigned,
    /// The Delivery was picked up, cancelled or failed.
    DeliveryClosed,
}

impl WakeReason {
    #[must_use]
    pub fn as_str(self) -> &'static str {
        match self {
            Self::OfferMade => "offer_made",
            Self::OfferAnswered => "offer_answered",
            Self::CourierAvailable => "courier_available",
            Self::CourierUnassigned => "courier_unassigned",
            Self::DeliveryClosed => "delivery_closed",
        }
    }
}

/// Start or signal failed; the commit already happened, the caller repeats
/// the call.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct AssignmentProcessError(pub String);

impl Display for AssignmentProcessError {
    fn fmt(&self, formatter: &mut Formatter<'_>) -> std::fmt::Result {
        write!(formatter, "assignment workflow unavailable: {}", self.0)
    }
}

pub trait AssignmentProcess {
    /// Starts the workflow of the Delivery; a running or finished workflow
    /// of the Delivery is left as it is.
    fn start(&self, delivery_id: &DeliveryId) -> Result<(), AssignmentProcessError>;

    /// Wakes the workflow of the Delivery; a finished workflow is not an
    /// error.
    fn wake(
        &self,
        delivery_id: &DeliveryId,
        reason: WakeReason,
    ) -> Result<(), AssignmentProcessError>;
}

/// Query of the Delivery a Courier Candidate wakes (FR-706). Implemented by
/// the PostgreSQL store.
pub trait CandidateDeliveries {
    /// The oldest waiting Delivery by `(created_at, id)` in the Market of the
    /// courier without a pending offer that was never offered to the courier.
    fn oldest_waiting_for(
        &self,
        courier_id: &CourierId,
    ) -> Result<Option<DeliveryId>, RepositoryError>;
}

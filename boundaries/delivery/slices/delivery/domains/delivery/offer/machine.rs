//! Pure core of the `DeliveryOffer` state machine, without dependencies and
//! iterators: `verification/` extracts this file to Lean and proves its
//! invariants (`docs/testing.md`).

/// Business state of a Delivery Offer. A pending offer past its `expires_at`
/// is already closed for acceptance and rejection; the `Expire` transition
/// records that closure as the stored state `Expired`.
#[derive(Clone, Copy, Debug, Eq, Hash, PartialEq)]
pub enum OfferState {
    Pending,
    Accepted,
    Rejected,
    /// Closed because the delivery was assigned through another offer.
    Withdrawn,
    /// Closed because nobody answered before `expires_at`.
    Expired,
}

impl OfferState {
    /// No trigger of the transition table leaves the state.
    #[must_use]
    pub fn is_terminal(self) -> bool {
        !(allowed(self, OfferTrigger::Accept)
            || allowed(self, OfferTrigger::Reject)
            || allowed(self, OfferTrigger::Withdraw)
            || allowed(self, OfferTrigger::Expire))
    }
}

#[derive(Clone, Copy, Debug, Eq, Hash, PartialEq)]
pub enum OfferTrigger {
    Accept,
    Reject,
    Withdraw,
    /// Allowed only past `expires_at`; the time guard is checked by
    /// `DeliveryOffer::expire`.
    Expire,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct OfferTransitionResult {
    pub from: OfferState,
    pub trigger: OfferTrigger,
    pub to: OfferState,
}

#[derive(Clone, Copy)]
pub enum OfferOutcome {
    Allowed(OfferState),
    Invalid,
    AlreadyApplied,
}

/// The transition table: every state and trigger has exactly one outcome.
#[must_use]
pub fn offer_outcome(from: OfferState, trigger: OfferTrigger) -> OfferOutcome {
    use OfferOutcome::{Allowed, AlreadyApplied, Invalid};
    use OfferState::{Accepted, Expired, Pending, Rejected, Withdrawn};
    use OfferTrigger::{Accept, Expire, Reject, Withdraw};

    match (from, trigger) {
        (Pending, Accept) => Allowed(Accepted),
        (Pending, Reject) => Allowed(Rejected),
        (Pending, Withdraw) => Allowed(Withdrawn),
        (Pending, Expire) => Allowed(Expired),
        (Accepted, Accept) | (Rejected, Reject) | (Withdrawn, Withdraw) | (Expired, Expire) => {
            AlreadyApplied
        }
        (Accepted, Reject | Withdraw | Expire)
        | (Rejected, Accept | Withdraw | Expire)
        | (Withdrawn, Accept | Reject | Expire)
        | (Expired, Accept | Reject | Withdraw) => Invalid,
    }
}

fn allowed(from: OfferState, trigger: OfferTrigger) -> bool {
    match offer_outcome(from, trigger) {
        OfferOutcome::Allowed(_) => true,
        OfferOutcome::Invalid | OfferOutcome::AlreadyApplied => false,
    }
}

fn leads_to(from: OfferState, trigger: OfferTrigger, to: OfferState) -> bool {
    match offer_outcome(from, trigger) {
        OfferOutcome::Allowed(target) => target == to,
        OfferOutcome::Invalid | OfferOutcome::AlreadyApplied => false,
    }
}

/// Whether an offer may expire after `offered_at`: its deadline comes later.
#[must_use]
pub fn valid_expiry(offered_at_epoch_seconds: i64, expires_at_epoch_seconds: i64) -> bool {
    expires_at_epoch_seconds > offered_at_epoch_seconds
}

/// Whether `now` is past the deadline; the deadline itself is inclusive for
/// accept and reject.
#[must_use]
pub fn past_deadline(now_epoch_seconds: i64, expires_at_epoch_seconds: i64) -> bool {
    now_epoch_seconds > expires_at_epoch_seconds
}

/// Whether the offer can still be answered: pending and not past its deadline.
#[must_use]
pub fn open_at(state: OfferState, now_epoch_seconds: i64, expires_at_epoch_seconds: i64) -> bool {
    state == OfferState::Pending && !past_deadline(now_epoch_seconds, expires_at_epoch_seconds)
}

/// The allowed transition of the table from `from` to `to`, if exactly one
/// trigger leads there: how a stored state change reads as a transition.
#[must_use]
pub fn applied_offer_transition(from: OfferState, to: OfferState) -> Option<OfferTransitionResult> {
    let trigger = match (
        leads_to(from, OfferTrigger::Accept, to),
        leads_to(from, OfferTrigger::Reject, to),
        leads_to(from, OfferTrigger::Withdraw, to),
        leads_to(from, OfferTrigger::Expire, to),
    ) {
        (true, false, false, false) => OfferTrigger::Accept,
        (false, true, false, false) => OfferTrigger::Reject,
        (false, false, true, false) => OfferTrigger::Withdraw,
        (false, false, false, true) => OfferTrigger::Expire,
        _ => return None,
    };
    Some(OfferTransitionResult { from, trigger, to })
}

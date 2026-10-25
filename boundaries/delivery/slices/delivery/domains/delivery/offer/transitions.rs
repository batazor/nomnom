//! Transition table of the `DeliveryOffer` state machine.

use super::DeliveryOfferError;

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

#[cfg(test)]
pub(super) const ALL_OFFER_STATES: [OfferState; 5] = [
    OfferState::Pending,
    OfferState::Accepted,
    OfferState::Rejected,
    OfferState::Withdrawn,
    OfferState::Expired,
];

#[derive(Clone, Copy, Debug, Eq, Hash, PartialEq)]
pub enum OfferTrigger {
    Accept,
    Reject,
    Withdraw,
    /// Allowed only past `expires_at`; the time guard is checked by
    /// [`super::DeliveryOffer::expire`].
    Expire,
}

#[cfg(test)]
pub(super) const ALL_OFFER_TRIGGERS: [OfferTrigger; 4] = [
    OfferTrigger::Accept,
    OfferTrigger::Reject,
    OfferTrigger::Withdraw,
    OfferTrigger::Expire,
];

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct OfferTransitionResult {
    pub from: OfferState,
    pub trigger: OfferTrigger,
    pub to: OfferState,
}

#[derive(Clone, Copy)]
enum OfferOutcome {
    Allowed(OfferState),
    Invalid,
    AlreadyApplied,
}

#[derive(Clone, Copy)]
pub(super) struct OfferTransitionDefinition {
    pub(super) from: OfferState,
    pub(super) trigger: OfferTrigger,
    outcome: OfferOutcome,
}

pub(super) const OFFER_TRANSITION_TABLE: [OfferTransitionDefinition; 20] = [
    offer_definition(
        OfferState::Pending,
        OfferTrigger::Accept,
        OfferOutcome::Allowed(OfferState::Accepted),
    ),
    offer_definition(
        OfferState::Pending,
        OfferTrigger::Reject,
        OfferOutcome::Allowed(OfferState::Rejected),
    ),
    offer_definition(
        OfferState::Pending,
        OfferTrigger::Withdraw,
        OfferOutcome::Allowed(OfferState::Withdrawn),
    ),
    offer_definition(
        OfferState::Pending,
        OfferTrigger::Expire,
        OfferOutcome::Allowed(OfferState::Expired),
    ),
    offer_definition(
        OfferState::Accepted,
        OfferTrigger::Accept,
        OfferOutcome::AlreadyApplied,
    ),
    offer_definition(
        OfferState::Accepted,
        OfferTrigger::Reject,
        OfferOutcome::Invalid,
    ),
    offer_definition(
        OfferState::Accepted,
        OfferTrigger::Withdraw,
        OfferOutcome::Invalid,
    ),
    offer_definition(
        OfferState::Accepted,
        OfferTrigger::Expire,
        OfferOutcome::Invalid,
    ),
    offer_definition(
        OfferState::Rejected,
        OfferTrigger::Accept,
        OfferOutcome::Invalid,
    ),
    offer_definition(
        OfferState::Rejected,
        OfferTrigger::Reject,
        OfferOutcome::AlreadyApplied,
    ),
    offer_definition(
        OfferState::Rejected,
        OfferTrigger::Withdraw,
        OfferOutcome::Invalid,
    ),
    offer_definition(
        OfferState::Rejected,
        OfferTrigger::Expire,
        OfferOutcome::Invalid,
    ),
    offer_definition(
        OfferState::Withdrawn,
        OfferTrigger::Accept,
        OfferOutcome::Invalid,
    ),
    offer_definition(
        OfferState::Withdrawn,
        OfferTrigger::Reject,
        OfferOutcome::Invalid,
    ),
    offer_definition(
        OfferState::Withdrawn,
        OfferTrigger::Withdraw,
        OfferOutcome::AlreadyApplied,
    ),
    offer_definition(
        OfferState::Withdrawn,
        OfferTrigger::Expire,
        OfferOutcome::Invalid,
    ),
    offer_definition(
        OfferState::Expired,
        OfferTrigger::Accept,
        OfferOutcome::Invalid,
    ),
    offer_definition(
        OfferState::Expired,
        OfferTrigger::Reject,
        OfferOutcome::Invalid,
    ),
    offer_definition(
        OfferState::Expired,
        OfferTrigger::Withdraw,
        OfferOutcome::Invalid,
    ),
    offer_definition(
        OfferState::Expired,
        OfferTrigger::Expire,
        OfferOutcome::AlreadyApplied,
    ),
];

const fn offer_definition(
    from: OfferState,
    trigger: OfferTrigger,
    outcome: OfferOutcome,
) -> OfferTransitionDefinition {
    OfferTransitionDefinition {
        from,
        trigger,
        outcome,
    }
}

pub(super) fn offer_transition(
    from: OfferState,
    trigger: OfferTrigger,
) -> Result<OfferTransitionResult, DeliveryOfferError> {
    let Some(rule) = OFFER_TRANSITION_TABLE
        .iter()
        .find(|rule| rule.from == from && rule.trigger == trigger)
    else {
        return Err(DeliveryOfferError::InvalidTransition { from, trigger });
    };
    match rule.outcome {
        OfferOutcome::Invalid => Err(DeliveryOfferError::InvalidTransition { from, trigger }),
        OfferOutcome::AlreadyApplied => Err(DeliveryOfferError::AlreadyInState { state: from }),
        OfferOutcome::Allowed(to) => Ok(OfferTransitionResult { from, trigger, to }),
    }
}

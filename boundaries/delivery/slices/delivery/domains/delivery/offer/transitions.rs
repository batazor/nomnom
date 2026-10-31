//! Errors of the `DeliveryOffer` transition table in [`super::machine`].

use super::DeliveryOfferError;
use super::machine::{
    OfferOutcome, OfferState, OfferTransitionResult, OfferTrigger, offer_outcome,
};

#[cfg(test)]
pub(super) const ALL_OFFER_STATES: [OfferState; 5] = [
    OfferState::Pending,
    OfferState::Accepted,
    OfferState::Rejected,
    OfferState::Withdrawn,
    OfferState::Expired,
];

#[cfg(test)]
pub(super) const ALL_OFFER_TRIGGERS: [OfferTrigger; 4] = [
    OfferTrigger::Accept,
    OfferTrigger::Reject,
    OfferTrigger::Withdraw,
    OfferTrigger::Expire,
];

pub(super) fn offer_transition(
    from: OfferState,
    trigger: OfferTrigger,
) -> Result<OfferTransitionResult, DeliveryOfferError> {
    match offer_outcome(from, trigger) {
        OfferOutcome::Invalid => Err(DeliveryOfferError::InvalidTransition { from, trigger }),
        OfferOutcome::AlreadyApplied => Err(DeliveryOfferError::AlreadyInState { state: from }),
        OfferOutcome::Allowed(to) => Ok(OfferTransitionResult { from, trigger, to }),
    }
}

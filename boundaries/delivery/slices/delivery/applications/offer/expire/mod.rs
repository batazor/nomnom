//! Expire Delivery Offer: the Assignment Workflow closes the pending offer
//! nobody answered when its timer fires at the deadline (FR-705). The offer
//! and its `DeliveryOfferExpired` fact are written in one Unit of Work. An
//! offer accepted, rejected or withdrawn before the timer — or still before
//! its deadline by the clock of the process — is left as it is: the next
//! step of the workflow reads the stored state. After commit the courier of an
//! expired offer wakes the search of its Market: it may take other deliveries
//! that found no candidate (FR-706). A repeat wakes it too: the wake of the
//! first call may have been lost, and Advance Assignment may have closed the
//! offer before the timer.
//!
//! The step runs inside the process, not on behalf of a caller, so there is
//! no subject and no permission check.

use crate::pkg::unit_of_work::UnitOfWork;
use crate::slices::courier::applications::candidate_wake::CandidateWake;
use crate::slices::delivery::applications::Clock;
use crate::slices::delivery::domains::delivery::{
    DeliveryOfferError, DeliveryOfferId, DeliveryOfferRepository, OfferState,
};

use super::super::error::DeliveryApplicationError;
use super::super::outbox::{DeliveryOutbox, publish};
use super::super::{load_offer, offer_id};

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct Input {
    pub offer_id: String,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct Output {
    pub offer_id: DeliveryOfferId,
    /// `false` when the offer was no longer pending or not yet past its
    /// deadline.
    pub expired: bool,
}

pub struct Handler<'a, U: UnitOfWork> {
    pub unit_of_work: &'a U,
    pub offers: &'a dyn DeliveryOfferRepository,
    pub outbox: &'a dyn DeliveryOutbox,
    pub clock: &'a dyn Clock,
    pub candidates: &'a dyn CandidateWake,
}

impl<U: UnitOfWork> Handler<'_, U> {
    pub fn execute(&self, input: Input) -> Result<Output, DeliveryApplicationError> {
        let offer_id = offer_id(&input.offer_id)?;
        let (output, closed) = self.unit_of_work.run(|| {
            let mut offer = load_offer(self.offers, &offer_id)?;
            let expired = match offer.expire(self.clock.now_epoch_seconds()) {
                Ok(_) => {
                    let facts = offer.pull_events();
                    self.offers.save(&offer)?;
                    publish(self.outbox, facts)?;
                    true
                }
                Err(
                    DeliveryOfferError::AlreadyInState { .. }
                    | DeliveryOfferError::InvalidTransition { .. }
                    | DeliveryOfferError::NotYetExpired,
                ) => false,
                Err(error) => return Err(DeliveryApplicationError::from(error)),
            };
            let output = Output {
                offer_id: offer_id.clone(),
                expired,
            };
            Ok((output, offer))
        })?;
        if closed.state() == OfferState::Expired {
            self.candidates.courier_available(closed.courier_id())?;
        }
        Ok(output)
    }
}

#[cfg(test)]
mod tests;

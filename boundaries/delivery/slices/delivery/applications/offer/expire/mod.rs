//! Expire Delivery Offer: the Assignment Workflow closes the pending offer
//! nobody answered when its timer fires at the deadline (FR-705). The offer
//! and its `DeliveryOfferExpired` fact are written in one Unit of Work. An
//! offer accepted, rejected or withdrawn before the timer — or still before
//! its deadline by the clock of the process — is left as it is: the next
//! step of the workflow reads the stored state.
//!
//! The step runs inside the process, not on behalf of a caller, so there is
//! no subject and no permission check.

use crate::pkg::unit_of_work::UnitOfWork;
use crate::pkg::usecase::Clock;
use crate::slices::delivery::domains::delivery::{
    DeliveryOfferError, DeliveryOfferId, DeliveryOfferRepository,
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
}

impl<U: UnitOfWork> Handler<'_, U> {
    pub fn execute(&self, input: Input) -> Result<Output, DeliveryApplicationError> {
        let offer_id = offer_id(&input.offer_id)?;
        self.unit_of_work.run(|| {
            let mut offer = load_offer(self.offers, &offer_id)?;
            match offer.expire(self.clock.now_epoch_seconds()) {
                Ok(_) => {}
                Err(
                    DeliveryOfferError::AlreadyInState { .. }
                    | DeliveryOfferError::InvalidTransition { .. }
                    | DeliveryOfferError::NotYetExpired,
                ) => {
                    return Ok(Output {
                        offer_id: offer_id.clone(),
                        expired: false,
                    });
                }
                Err(error) => return Err(error.into()),
            }
            let expired = offer.pull_events();
            self.offers.save(&offer)?;
            publish(self.outbox, expired)?;
            Ok(Output {
                offer_id: offer_id.clone(),
                expired: true,
            })
        })
    }
}

#[cfg(test)]
mod tests;

//! Expire Delivery Offers: the background job of the `delivery` process
//! closes pending Delivery Offers nobody answered before `expires_at`. One
//! call handles one batch: the oldest pending offers with `expires_at < now`,
//! at most `batch_size`. Every offer is expired in its own Unit of Work
//! together with its `DeliveryOfferExpired` fact in the Outbox. An offer that
//! was accepted, rejected or withdrawn concurrently (optimistic concurrency
//! conflict) is skipped, not an error. Re-offering the Delivery to another
//! courier is not part of this use case: Dispatch Deliveries does it.
//!
//! The job runs inside the process, not on behalf of a caller, so there is no
//! subject and no permission check.

use crate::pkg::application::{Clock, UnitOfWork};
use crate::slices::delivery::domains::delivery::{
    DeliveryOffer, DeliveryOfferError, DeliveryOfferId, DeliveryOfferRepository,
};

use super::error::DeliveryApplicationError;
use super::outbox::{DeliveryOutbox, publish};

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct Input {
    /// Maximum number of offers handled by this call; `0` handles none.
    pub batch_size: usize,
}

#[derive(Clone, Debug, Default, Eq, PartialEq)]
pub struct Output {
    /// Offers this call moved to `expired`, in the order handled.
    pub expired: Vec<DeliveryOfferId>,
    /// Offers that were closed concurrently and were left as they are.
    pub skipped: Vec<DeliveryOfferId>,
    /// `true` when the batch was full, so more expired offers may remain.
    pub more: bool,
}

pub struct Handler<'a, U: UnitOfWork> {
    pub unit_of_work: &'a U,
    pub offers: &'a dyn DeliveryOfferRepository,
    pub outbox: &'a dyn DeliveryOutbox,
    pub clock: &'a dyn Clock,
}

impl<U: UnitOfWork> Handler<'_, U> {
    pub fn execute(&self, input: Input) -> Result<Output, DeliveryApplicationError> {
        let mut output = Output::default();
        if input.batch_size == 0 {
            return Ok(output);
        }
        let now = self.clock.now_epoch_seconds();
        let batch = self.offers.list_expired_pending(now, input.batch_size)?;
        output.more = batch.len() >= input.batch_size;
        for offer in batch {
            let id = offer.id().clone();
            if self.expire(offer, now)? {
                output.expired.push(id);
            } else {
                output.skipped.push(id);
            }
        }
        Ok(output)
    }

    /// `false` when the offer was closed concurrently.
    fn expire(&self, mut offer: DeliveryOffer, now: i64) -> Result<bool, DeliveryApplicationError> {
        match offer.expire(now) {
            Ok(_) => {}
            // The snapshot is no longer pending or no longer past its deadline.
            Err(
                DeliveryOfferError::AlreadyInState { .. }
                | DeliveryOfferError::InvalidTransition { .. }
                | DeliveryOfferError::NotYetExpired,
            ) => return Ok(false),
            Err(error) => return Err(error.into()),
        }
        let outcome = self.unit_of_work.run(|| {
            let expired = offer.pull_events();
            self.offers.save(&offer)?;
            publish(self.outbox, expired)?;
            Ok::<(), DeliveryApplicationError>(())
        });
        match outcome {
            Ok(()) => Ok(true),
            // Accepted, rejected or withdrawn after the batch was read.
            Err(DeliveryApplicationError::ConcurrentModification) => Ok(false),
            Err(error) => Err(error),
        }
    }
}

#[cfg(test)]
mod tests;

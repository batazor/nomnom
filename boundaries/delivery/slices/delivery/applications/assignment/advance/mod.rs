//! Advance Assignment: one step of the Assignment Workflow of a Delivery
//! (FR-704, FR-705). The workflow keeps no state of the Delivery: every step
//! reads the stored one and says what the workflow waits for next.
//!
//! - a Delivery picked up or closed finishes the workflow;
//! - an assigned Delivery waits for a wake (Reassign, Pickup, Cancel);
//! - a waiting Delivery with an open offer waits for its answer or deadline;
//! - a waiting Delivery without an open offer expires its pending offers
//!   past the deadline, selects a courier through Find Courier
//!   ([`select_candidate`]) — couriers already offered are skipped — and
//!   offers it to the candidate ([`make_offer`]), all in one Unit of Work.
//!   Without a candidate it waits for a courier to become available.
//!
//! The Delivery is saved with the version it was read at before the offer is
//! made, so a concurrent change — an accepted offer, Reassign, Cancel — is an
//! optimistic concurrency conflict, as is a courier that stopped being
//! available after it was selected. The workflow repeats the step.
//!
//! The step runs inside the process, not on behalf of a caller, so there is
//! no subject and no permission check.

use crate::pkg::unit_of_work::UnitOfWork;
use crate::pkg::usecase::{Clock, IdGenerator};
use crate::slices::courier::applications::assignment::CourierAssignment;
use crate::slices::delivery::applications::offers_of_delivery::{OffersOfDelivery, load_offers_of};
use crate::slices::delivery::domains::delivery::{
    DeliveryId, DeliveryOfferId, DeliveryOfferRepository, DeliveryRepository, OfferState, State,
};

use super::super::courier_locator::CourierLocator;
use super::super::error::DeliveryApplicationError;
use super::super::offer::offer_delivery::{OfferPorts, make_offer};
use super::super::outbox::{DeliveryOutbox, publish};
use super::super::policy::AssignmentPolicy;
use super::super::{delivery_id, load_delivery};
use super::find_courier::{Outcome, select_candidate};

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct Input {
    pub delivery_id: String,
}

/// What the workflow waits for after the step.
#[derive(Clone, Debug, Eq, PartialEq)]
pub enum Output {
    /// The offer is open until `expires_at_epoch_seconds` inclusive: wait for
    /// a wake or the deadline.
    Offered {
        offer_id: DeliveryOfferId,
        expires_at_epoch_seconds: i64,
    },
    /// No available courier who was not offered the Delivery yet: wait for
    /// a wake.
    NoCandidate,
    /// A courier is assigned: wait for a wake.
    Assigned,
    /// Picked up, delivered, failed or cancelled: the workflow ends.
    Finished,
}

pub struct Handler<'a, U: UnitOfWork> {
    pub unit_of_work: &'a U,
    pub deliveries: &'a dyn DeliveryRepository,
    pub couriers: &'a dyn CourierAssignment,
    pub offers: &'a dyn DeliveryOfferRepository,
    pub offers_of: &'a dyn OffersOfDelivery,
    pub outbox: &'a dyn DeliveryOutbox,
    pub locator: &'a dyn CourierLocator,
    pub ids: &'a dyn IdGenerator,
    pub clock: &'a dyn Clock,
    pub policy: AssignmentPolicy,
}

impl<U: UnitOfWork> Handler<'_, U> {
    pub fn execute(&self, input: Input) -> Result<Output, DeliveryApplicationError> {
        let delivery_id = delivery_id(&input.delivery_id)?;
        self.unit_of_work.run(|| self.advance(&delivery_id))
    }

    fn advance(&self, delivery_id: &DeliveryId) -> Result<Output, DeliveryApplicationError> {
        let delivery = load_delivery(self.deliveries, delivery_id)?;
        match delivery.state() {
            State::Waiting => {}
            State::Assigned => return Ok(Output::Assigned),
            State::PickedUp | State::Delivered | State::Failed | State::Cancelled => {
                return Ok(Output::Finished);
            }
        }

        let now = self.clock.now_epoch_seconds();
        for mut offer in load_offers_of(self.offers_of, self.offers, delivery_id)? {
            if offer.is_open_at(now) {
                return Ok(Output::Offered {
                    offer_id: offer.id().clone(),
                    expires_at_epoch_seconds: offer.expires_at_epoch_seconds(),
                });
            }
            // Pending past its deadline: the timer of the workflow has not
            // fired yet, the step closes it before the next offer (FR-705).
            if offer.state() == OfferState::Pending {
                offer.expire(now)?;
                let expired = offer.pull_events();
                self.offers.save(&offer)?;
                publish(self.outbox, expired)?;
            }
        }

        let selection = select_candidate(
            &delivery,
            self.offers_of,
            self.offers,
            self.couriers,
            self.locator,
            self.policy,
        )?;
        let Outcome::CandidateFound { courier_id, .. } = selection.outcome else {
            return Ok(Output::NoCandidate);
        };
        // Claims the delivery at the version it was read at.
        self.deliveries.save(&delivery)?;
        let offer = make_offer(
            &delivery,
            courier_id,
            &OfferPorts {
                couriers: self.couriers,
                offers: self.offers,
                offers_of: self.offers_of,
                outbox: self.outbox,
                ids: self.ids,
                clock: self.clock,
                policy: self.policy,
            },
        )?;
        Ok(Output::Offered {
            offer_id: offer.offer_id,
            expires_at_epoch_seconds: offer.expires_at_epoch_seconds,
        })
    }
}

#[cfg(test)]
mod tests;

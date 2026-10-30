//! Accept Delivery Offer: the addressee accepts a current offer. Exactly one
//! offer of a Delivery becomes the Courier Assignment: the delivery is
//! assigned, the courier becomes busy, the remaining pending offers are
//! withdrawn and the assignment fact is written to the Outbox in one Unit of
//! Work. Before commit `delivery:<id>#courier@user:<courier_id>` is written to
//! the authorization graph; a failed write rolls the acceptance back. A repeated
//! acceptance writes nothing. The courier may name the expected Pickup time;
//! a named one must be in the future (FR-610) and reaches
//! `CourierAssignedToDelivery`. After commit, and on a repeat, the Assignment
//! Workflow of the delivery is woken (FR-707).

use crate::pkg::authz::Subject;
use crate::pkg::unit_of_work::UnitOfWork;
use crate::pkg::usecase::Clock;
use crate::slices::courier::applications::assignment::CourierAssignment;
use crate::slices::courier::domains::courier::CourierId;
use crate::slices::delivery::applications::offers_of_delivery::{OffersOfDelivery, load_offers_of};
use crate::slices::delivery::domains::delivery::{
    DeliveryId, DeliveryOffer, DeliveryOfferId, DeliveryOfferRepository, DeliveryRepository,
    ExpectedTime, OfferAcceptanceConditions, OfferState,
};

use super::assignment_process::{AssignmentProcess, WakeReason};
use super::authorization::{DeliveryAuthorization, DeliveryPermission, DeliveryResource};
use super::courier_locator::CourierLocator;
use super::error::DeliveryApplicationError;
use super::outbox::{DeliveryOutbox, publish};
use super::policy::AssignmentPolicy;
use super::relationships::DeliveryRelationshipWriter;
use super::{courier_id, expected_time, load_delivery, load_offer, offer_id};

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct Input {
    pub offer_id: String,
    pub courier_id: String,
    /// Pickup time the courier names; `None` — not named.
    pub expected_pickup_at_epoch_seconds: Option<i64>,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct Output {
    pub offer_id: DeliveryOfferId,
    pub delivery_id: DeliveryId,
    pub courier_id: CourierId,
    /// `true` when the same courier had already accepted this offer.
    pub already_accepted: bool,
}

pub struct Handler<'a, U: UnitOfWork> {
    pub authorization: &'a dyn DeliveryAuthorization,
    pub unit_of_work: &'a U,
    pub offers: &'a dyn DeliveryOfferRepository,
    pub offers_of: &'a dyn OffersOfDelivery,
    pub deliveries: &'a dyn DeliveryRepository,
    pub couriers: &'a dyn CourierAssignment,
    pub outbox: &'a dyn DeliveryOutbox,
    pub locator: &'a dyn CourierLocator,
    pub clock: &'a dyn Clock,
    pub policy: AssignmentPolicy,
    pub relationships: &'a dyn DeliveryRelationshipWriter,
    pub assignment: &'a dyn AssignmentProcess,
}

impl<U: UnitOfWork> Handler<'_, U> {
    pub fn execute(
        &self,
        subject: &Subject,
        input: Input,
    ) -> Result<Output, DeliveryApplicationError> {
        let offer_id = offer_id(&input.offer_id)?;
        let courier_id = courier_id(&input.courier_id)?;
        let expected_pickup_at = expected_time(
            "expected_pickup_at",
            input.expected_pickup_at_epoch_seconds,
            self.clock,
        )?;
        self.authorization.check(
            subject,
            DeliveryPermission::AcceptDeliveryOffer,
            &DeliveryResource::Courier(courier_id.clone()),
        )?;

        let offer = load_offer(self.offers, &offer_id)?;
        if is_repeat(&offer, &courier_id) {
            // The wake of the first call may have been lost.
            self.assignment
                .wake(offer.delivery_id(), WakeReason::OfferAnswered)?;
            return Ok(output(&offer, true));
        }
        // Values from outside the transaction are gathered before it starts.
        let snapshot = self
            .locator
            .snapshot(&courier_id)?
            .ok_or_else(|| DeliveryApplicationError::CourierPositionUnknown(courier_id.clone()))?;
        let conditions = OfferAcceptanceConditions {
            now_epoch_seconds: self.clock.now_epoch_seconds(),
            distance_to_pickup_meters: snapshot.distance_to_pickup_meters,
            maximum_distance_meters: self.policy.maximum_distance_meters,
            active_assignments: snapshot.active_assignments,
            maximum_active_assignments: self.policy.maximum_active_assignments,
        };

        let output = self
            .unit_of_work
            .run(|| self.accept(&offer_id, &courier_id, conditions, expected_pickup_at))?;
        self.assignment
            .wake(&output.delivery_id, WakeReason::OfferAnswered)?;
        Ok(output)
    }

    fn accept(
        &self,
        offer_id: &DeliveryOfferId,
        courier_id: &CourierId,
        conditions: OfferAcceptanceConditions,
        expected_pickup_at: Option<ExpectedTime>,
    ) -> Result<Output, DeliveryApplicationError> {
        let mut offer = load_offer(self.offers, offer_id)?;
        if is_repeat(&offer, courier_id) {
            return Ok(output(&offer, true));
        }
        let mut delivery = load_delivery(self.deliveries, offer.delivery_id())?;

        offer.accept(courier_id, conditions)?;
        delivery.assign(courier_id.clone(), expected_pickup_at)?;
        // The courier becomes busy through the `courier` slice port in this
        // Unit of Work (FR-310, FR-485).
        self.couriers.occupy(courier_id, delivery.market_id())?;
        // The Courier Assignment is valid; grant access before commit and
        // check the delivery at least as fresh as this write (FR-447).
        let revision = self
            .relationships
            .assign_courier(delivery.id(), courier_id)?;
        delivery.record_authorization_revision(revision);

        for mut other in load_offers_of(self.offers_of, self.offers, delivery.id())? {
            if other.id() != offer.id() && other.state() == OfferState::Pending {
                other.withdraw()?;
                self.offers.save(&other)?;
            }
        }
        let accepted = offer.pull_events();
        self.offers.save(&offer)?;
        self.deliveries.save(&delivery)?;
        publish(self.outbox, accepted)?;
        publish(self.outbox, delivery.pull_events())?;
        Ok(output(&offer, false))
    }
}

fn is_repeat(offer: &DeliveryOffer, courier_id: &CourierId) -> bool {
    offer.state() == OfferState::Accepted && offer.courier_id() == courier_id
}

fn output(offer: &DeliveryOffer, already_accepted: bool) -> Output {
    Output {
        offer_id: offer.id().clone(),
        delivery_id: offer.delivery_id().clone(),
        courier_id: offer.courier_id().clone(),
        already_accepted,
    }
}

#[cfg(test)]
mod tests;

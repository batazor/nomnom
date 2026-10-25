//! Replan Delivery: the fulfillment process of `orders` asks `delivery` to
//! plan the Delivery of an order again. If a courier is assigned, the Courier
//! Assignment is ended; then the nearest eligible courier is selected and
//! receives a Delivery Offer. No candidate is a success without an offer.
//!
//! The permission is checked once (`ReplanDelivery` on the order, AC-633);
//! the steps run as the application services of Reassign Courier
//! ([`end_assignment`]), Find Courier ([`select_candidate`]) and Offer
//! Delivery ([`make_offer`]) in one Unit of Work, so a failed step leaves
//! the assignment in place. The relationship DELETE is not transactional: if
//! a later step fails, reconciliation restores the missing relationship
//! (FR-445).

use crate::pkg::application::{Clock, IdGenerator, Subject, UnitOfWork};
use crate::slices::courier::applications::assignment::CourierAssignment;
use crate::slices::courier::domains::courier::CourierId;
use crate::slices::delivery::applications::offers_of_delivery::OffersOfDelivery;
use crate::slices::delivery::domains::delivery::{
    DeliveryId, DeliveryOfferRepository, DeliveryRepository,
};

use super::authorization::{DeliveryAuthorization, DeliveryPermission, DeliveryResource};
use super::courier_locator::CourierLocator;
use super::error::DeliveryApplicationError;
use super::find_courier::{Outcome, select_candidate};
use super::offer_delivery::{self, OfferPorts, make_offer};
use super::outbox::DeliveryOutbox;
use super::policy::AssignmentPolicy;
use super::reassign_courier::end_assignment;
use super::relationships::DeliveryRelationshipWriter;
use super::{load_delivery_by_order, order_id};

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct Input {
    pub order_id: String,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct Output {
    pub delivery_id: DeliveryId,
    /// Courier whose assignment this replan ended, if any.
    pub released_courier_id: Option<CourierId>,
    /// Offer made by this replan; `None` when no candidate was found.
    pub offer: Option<offer_delivery::Output>,
}

pub struct Handler<'a, U: UnitOfWork> {
    pub authorization: &'a dyn DeliveryAuthorization,
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
    pub relationships: &'a dyn DeliveryRelationshipWriter,
}

impl<U: UnitOfWork> Handler<'_, U> {
    pub fn execute(
        &self,
        subject: &Subject,
        input: Input,
    ) -> Result<Output, DeliveryApplicationError> {
        let order_id = order_id(&input.order_id)?;
        self.authorization.check(
            subject,
            DeliveryPermission::ReplanDelivery,
            &DeliveryResource::Order(order_id.clone()),
        )?;

        self.unit_of_work.run(|| {
            let mut delivery = load_delivery_by_order(self.deliveries, &order_id)?;
            let released_courier_id = if delivery.courier_id().is_some() {
                Some(end_assignment(
                    &mut delivery,
                    self.deliveries,
                    self.couriers,
                    self.outbox,
                    self.relationships,
                )?)
            } else {
                None
            };

            let selection = select_candidate(
                &delivery,
                self.offers_of,
                self.offers,
                self.couriers,
                self.locator,
                self.policy,
            )?;
            let offer = match selection.outcome {
                Outcome::CandidateFound { courier_id, .. } => Some(make_offer(
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
                )?),
                Outcome::NoCandidate => None,
            };

            Ok(Output {
                delivery_id: delivery.id().clone(),
                released_courier_id,
                offer,
            })
        })
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::infrastructure::memory::{
        RecordingRelationships, RelationshipWrite, StaticAuthorization,
    };
    use crate::infrastructure::postgres::test_support::TestStore;
    use crate::slices::courier::domains::courier::CourierStatus;
    use crate::slices::delivery::applications::test_support::{World, candidate, service};
    use crate::slices::delivery::domains::delivery::{
        DeliveryError, DeliveryOfferId, OfferState, OrderId, State, TransitionError, Trigger,
    };

    fn handler(world: &World) -> Handler<'_, TestStore> {
        Handler {
            authorization: &world.authorization,
            unit_of_work: &world.store,
            deliveries: &world.store,
            couriers: &world.store,
            offers: &world.store,
            offers_of: &world.store,
            outbox: &world.store,
            locator: &world.locator,
            ids: &world.ids,
            clock: &world.clock,
            policy: world.policy,
            relationships: &world.relationships,
        }
    }

    fn input() -> Input {
        Input {
            order_id: "order-of-delivery-1".to_owned(),
        }
    }

    #[test]
    fn waiting_delivery_is_offered_to_the_candidate() {
        let world = World::new();
        world.waiting_delivery("delivery-1");
        world.courier("courier-1", CourierStatus::Available);
        world
            .locator
            .set_candidates(vec![candidate("courier-1", 500)]);

        let output = handler(&world)
            .execute(&service(), input())
            .expect("replanned");

        assert_eq!(output.delivery_id, DeliveryId::new("delivery-1"));
        assert_eq!(output.released_courier_id, None);
        let offer = output.offer.expect("offer made");
        assert_eq!(offer.courier_id, CourierId::new("courier-1"));
        assert_eq!(offer.offer_id, DeliveryOfferId::new("generated-1"));
        assert_eq!(world.offer_state("generated-1"), OfferState::Pending);
        assert_eq!(world.delivery_state("delivery-1"), State::Waiting);
    }

    #[test]
    fn assigned_delivery_is_reassigned_then_offered() {
        let world = World::new();
        world.assigned_delivery("delivery-1", "courier-1");
        world.courier("courier-2", CourierStatus::Available);
        world.locator.set_candidates(vec![
            candidate("courier-1", 1_000),
            candidate("courier-2", 500),
        ]);

        let output = handler(&world)
            .execute(&service(), input())
            .expect("replanned");

        assert_eq!(
            output.released_courier_id,
            Some(CourierId::new("courier-1"))
        );
        assert_eq!(
            output.offer.map(|offer| offer.courier_id),
            Some(CourierId::new("courier-2"))
        );
        let delivery = world.delivery("delivery-1");
        assert_eq!(delivery.state(), State::Waiting);
        assert_eq!(delivery.courier_id(), None);
        assert_eq!(world.courier_status("courier-1"), CourierStatus::Available);
        // An offer is not an assignment: only the released courier loses access.
        assert_eq!(
            world.relationships.writes(),
            vec![RelationshipWrite::UnassignCourier(
                DeliveryId::new("delivery-1"),
                CourierId::new("courier-1")
            )]
        );
        // FR-447: Replan stores the revision of the reassignment DELETE.
        assert_eq!(
            delivery.authorization_revision(),
            Some(&RecordingRelationships::revision(1))
        );
    }

    /// AC-633: one permission check and one Unit of Work for all steps.
    #[test]
    fn replan_checks_permission_once_in_one_unit_of_work() {
        let world = World::new();
        world.assigned_delivery("delivery-1", "courier-1");
        world.courier("courier-2", CourierStatus::Available);
        world
            .locator
            .set_candidates(vec![candidate("courier-2", 500)]);

        handler(&world)
            .execute(&service(), input())
            .expect("replanned");

        assert_eq!(
            world.authorization.delivery_checks(),
            vec![(
                service(),
                DeliveryPermission::ReplanDelivery,
                DeliveryResource::Order(OrderId::new("order-of-delivery-1"))
            )]
        );
        assert_eq!(world.store.commits(), 1);
    }

    /// AC-633: a failed search rolls the ended assignment back.
    #[test]
    fn failed_search_keeps_the_assignment() {
        let world = World::new();
        world.assigned_delivery("delivery-1", "courier-1");
        world.locator.set_unavailable(true);

        assert!(matches!(
            handler(&world).execute(&service(), input()),
            Err(DeliveryApplicationError::DependencyUnavailable(_))
        ));
        assert_eq!(world.delivery_state("delivery-1"), State::Assigned);
        assert_eq!(world.courier_status("courier-1"), CourierStatus::Busy);
        assert!(world.store.outbox_messages().is_empty());
        // SpiceDB is outside the transaction: the DELETE written before the
        // failure leaves a missing relationship for reconciliation (FR-445).
        assert_eq!(
            world.relationships.writes(),
            vec![RelationshipWrite::UnassignCourier(
                DeliveryId::new("delivery-1"),
                CourierId::new("courier-1")
            )]
        );
    }

    #[test]
    fn no_candidate_is_success_without_offer() {
        let world = World::new();
        world.waiting_delivery("delivery-1");

        let output = handler(&world)
            .execute(&service(), input())
            .expect("replanned");

        assert_eq!(output.offer, None);
        assert_eq!(world.delivery_state("delivery-1"), State::Waiting);
    }

    #[test]
    fn picked_up_delivery_cannot_be_replanned() {
        let world = World::new();
        world.picked_up_delivery("delivery-1", "courier-1");

        assert_eq!(
            handler(&world).execute(&service(), input()),
            Err(DeliveryApplicationError::DeliveryPrecondition(
                DeliveryError::Transition(TransitionError::InvalidTransition {
                    from: State::PickedUp,
                    trigger: Trigger::Unassign,
                })
            ))
        );
        assert_eq!(world.delivery_state("delivery-1"), State::PickedUp);
    }

    #[test]
    fn unknown_order_is_not_found() {
        let world = World::new();
        assert_eq!(
            handler(&world).execute(&service(), input()),
            Err(DeliveryApplicationError::DeliveryForOrderNotFound(
                OrderId::new("order-of-delivery-1")
            ))
        );
    }

    #[test]
    fn denied_subject_replans_nothing() {
        let world = World::with_authorization(StaticAuthorization::deny_all());
        world.assigned_delivery("delivery-1", "courier-1");

        assert_eq!(
            handler(&world).execute(&service(), input()),
            Err(DeliveryApplicationError::PermissionDenied)
        );
        assert_eq!(world.delivery_state("delivery-1"), State::Assigned);
    }
}

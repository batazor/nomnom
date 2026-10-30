//! Offer Delivery To Courier: a dispatcher of the Courier Pool offers the
//! waiting Delivery of an order to the courier they chose. The offer is the
//! same time-limited `Delivery Offer` the assignment process makes; the
//! courier accepts or rejects it, and until then other offers of the delivery
//! stay open. The offer and its `DeliveryOffered` fact are written in one
//! Unit of Work by [`make_offer`]. After commit the Assignment Workflow of the
//! delivery is woken, so it waits for the answer or the deadline of the offer.

use crate::pkg::authz::Subject;
use crate::pkg::unit_of_work::UnitOfWork;
use crate::pkg::usecase::{Clock, IdGenerator};
use crate::slices::courier::applications::assignment::CourierAssignment;
use crate::slices::delivery::applications::offers_of_delivery::OffersOfDelivery;
use crate::slices::delivery::domains::delivery::{DeliveryOfferRepository, DeliveryRepository};

use super::assignment_process::{AssignmentProcess, WakeReason};
use super::authorization::{DeliveryAuthorization, DeliveryPermission, DeliveryResource};
use super::error::DeliveryApplicationError;
use super::offer_delivery::{OfferPorts, Output, make_offer};
use super::outbox::DeliveryOutbox;
use super::policy::AssignmentPolicy;
use super::{courier_id, load_delivery_by_order, order_id};

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct Input {
    pub order_id: String,
    pub courier_id: String,
}

pub struct Handler<'a, U: UnitOfWork> {
    pub authorization: &'a dyn DeliveryAuthorization,
    pub unit_of_work: &'a U,
    pub deliveries: &'a dyn DeliveryRepository,
    pub couriers: &'a dyn CourierAssignment,
    pub offers: &'a dyn DeliveryOfferRepository,
    pub offers_of: &'a dyn OffersOfDelivery,
    pub outbox: &'a dyn DeliveryOutbox,
    pub ids: &'a dyn IdGenerator,
    pub clock: &'a dyn Clock,
    pub policy: AssignmentPolicy,
    pub assignment: &'a dyn AssignmentProcess,
}

impl<U: UnitOfWork> Handler<'_, U> {
    pub fn execute(
        &self,
        subject: &Subject,
        input: Input,
    ) -> Result<Output, DeliveryApplicationError> {
        let order_id = order_id(&input.order_id)?;
        let courier_id = courier_id(&input.courier_id)?;
        self.authorization.check(
            subject,
            DeliveryPermission::OfferDeliveryToCourier,
            &DeliveryResource::Platform,
        )?;

        let offer = self.unit_of_work.run(|| {
            let delivery = load_delivery_by_order(self.deliveries, &order_id)?;
            make_offer(
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
            )
        })?;
        self.assignment
            .wake(&offer.delivery_id, WakeReason::OfferMade)?;
        Ok(offer)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::infrastructure::memory::StaticAuthorization;
    use crate::infrastructure::repository::test_support::TestStore;
    use crate::slices::courier::domains::courier::{CourierError, CourierId, CourierStatus};
    use crate::slices::delivery::applications::test_support::{
        NOW, OFFER_TTL, World, hamburg, subject,
    };
    use crate::slices::delivery::domains::delivery::{
        DeliveryError, DeliveryId, DeliveryOfferId, OfferState, OrderId, State, TransitionError,
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
            ids: &world.ids,
            clock: &world.clock,
            policy: world.policy,
            assignment: &world.assignment,
        }
    }

    fn input(courier: &str) -> Input {
        Input {
            order_id: "order-of-delivery-1".to_owned(),
            courier_id: courier.to_owned(),
        }
    }

    #[test]
    fn dispatcher_offers_the_order_to_the_chosen_courier() {
        let world = World::new();
        world.waiting_delivery("delivery-1");
        world.courier("courier-near", CourierStatus::Available);
        world.courier("courier-chosen", CourierStatus::Available);
        world.offer("offer-auto", "delivery-1", "courier-near");

        let output = handler(&world)
            .execute(&subject("dispatcher"), input("courier-chosen"))
            .expect("offer created");

        assert_eq!(
            output,
            Output {
                offer_id: DeliveryOfferId::new("generated-1"),
                delivery_id: DeliveryId::new("delivery-1"),
                courier_id: CourierId::new("courier-chosen"),
                expires_at_epoch_seconds: NOW + OFFER_TTL,
                created: true,
            }
        );
        assert_eq!(world.offer_state("generated-1"), OfferState::Pending);
        // The offer of the assignment process stays: the first acceptance wins.
        assert_eq!(world.offer_state("offer-auto"), OfferState::Pending);
        assert_eq!(world.delivery_state("delivery-1"), State::Waiting);
        assert_eq!(
            world.authorization.delivery_checks(),
            vec![(
                subject("dispatcher"),
                DeliveryPermission::OfferDeliveryToCourier,
                DeliveryResource::Platform,
            )]
        );
    }

    #[test]
    fn busy_courier_gets_no_offer() {
        let world = World::new();
        world.waiting_delivery("delivery-1");
        world.courier("busy", CourierStatus::Busy);

        assert_eq!(
            handler(&world).execute(&subject("dispatcher"), input("busy")),
            Err(DeliveryApplicationError::CourierPrecondition(
                CourierError::AlreadyBusy
            ))
        );
        assert!(world.offers_of("delivery-1").expect("read").is_empty());
    }

    #[test]
    fn courier_of_another_market_gets_no_offer() {
        let world = World::new();
        world.waiting_delivery("delivery-1");
        world.courier_in("courier-h", CourierStatus::Available, &hamburg());

        assert_eq!(
            handler(&world).execute(&subject("dispatcher"), input("courier-h")),
            Err(DeliveryApplicationError::CourierPrecondition(
                CourierError::OtherMarket
            ))
        );
    }

    #[test]
    fn assigned_order_is_not_offered() {
        let world = World::new();
        world.assigned_delivery("delivery-1", "courier-1");
        world.courier("courier-2", CourierStatus::Available);

        assert_eq!(
            handler(&world).execute(&subject("dispatcher"), input("courier-2")),
            Err(DeliveryApplicationError::DeliveryPrecondition(
                DeliveryError::Transition(TransitionError::AlreadyInState {
                    state: State::Assigned
                })
            ))
        );
    }

    #[test]
    fn order_without_delivery_is_not_found() {
        let world = World::new();
        world.courier("courier-1", CourierStatus::Available);

        assert_eq!(
            handler(&world).execute(&subject("dispatcher"), input("courier-1")),
            Err(DeliveryApplicationError::DeliveryForOrderNotFound(
                OrderId::new("order-of-delivery-1")
            ))
        );
    }

    #[test]
    fn user_who_may_not_dispatch_creates_no_offer() {
        let world = World::with_authorization(StaticAuthorization::deny_all());
        world.waiting_delivery("delivery-1");
        world.courier("courier-1", CourierStatus::Available);

        assert_eq!(
            handler(&world).execute(&subject("stranger"), input("courier-1")),
            Err(DeliveryApplicationError::PermissionDenied)
        );
        assert!(world.offers_of("delivery-1").expect("read").is_empty());
    }
}

//! Replan Delivery: the fulfillment process of `orders` asks `delivery` to
//! plan the Delivery of an order again. If a courier is assigned, the Courier
//! Assignment is ended (Reassign Courier); then the nearest eligible courier
//! is selected (Find Courier) and receives a Delivery Offer (Offer Delivery).
//! No candidate is a success without an offer. The selection algorithm stays
//! inside `delivery`; this use case only composes the existing handlers.

use crate::pkg::application::{Clock, IdGenerator, Subject, UnitOfWork};
use crate::slices::courier::applications::assignment::CourierAssignment;
use crate::slices::courier::domains::courier::CourierId;
use crate::slices::delivery::domains::delivery::{
    DeliveryId, DeliveryOfferRepository, DeliveryRepository,
};

use super::authorization::{DeliveryAuthorization, DeliveryPermission, DeliveryResource};
use super::courier_locator::CourierLocator;
use super::error::DeliveryApplicationError;
use super::outbox::DeliveryOutbox;
use super::policy::AssignmentPolicy;
use super::relationships::DeliveryRelationshipWriter;
use super::{find_courier, load_delivery_by_order, offer_delivery, order_id, reassign_courier};

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

        let delivery = load_delivery_by_order(self.deliveries, &order_id)?;
        let delivery_id = delivery.id().clone();

        let released_courier_id = if delivery.courier_id().is_some() {
            let reassigned = reassign_courier::Handler {
                authorization: self.authorization,
                unit_of_work: self.unit_of_work,
                deliveries: self.deliveries,
                couriers: self.couriers,
                outbox: self.outbox,
                relationships: self.relationships,
            }
            .execute(
                subject,
                reassign_courier::Input {
                    delivery_id: delivery_id.as_str().to_owned(),
                },
            )?;
            Some(reassigned.released_courier_id)
        } else {
            None
        };

        let search = find_courier::Handler {
            authorization: self.authorization,
            deliveries: self.deliveries,
            couriers: self.couriers,
            offers: self.offers,
            locator: self.locator,
            policy: self.policy,
        }
        .execute(
            subject,
            find_courier::Input {
                delivery_id: delivery_id.as_str().to_owned(),
            },
        )?;

        let offer = match search.outcome {
            find_courier::Outcome::CandidateFound { courier_id, .. } => Some(
                offer_delivery::Handler {
                    authorization: self.authorization,
                    deliveries: self.deliveries,
                    couriers: self.couriers,
                    offers: self.offers,
                    ids: self.ids,
                    clock: self.clock,
                    policy: self.policy,
                }
                .execute(
                    subject,
                    offer_delivery::Input {
                        delivery_id: delivery_id.as_str().to_owned(),
                        courier_id: courier_id.as_str().to_owned(),
                    },
                )?,
            ),
            find_courier::Outcome::NoCandidate => None,
        };

        Ok(Output {
            delivery_id,
            released_courier_id,
            offer,
        })
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::infrastructure::memory::{
        MemoryStore, RecordingRelationships, RelationshipWrite, StaticAuthorization,
    };
    use crate::slices::courier::domains::courier::CourierStatus;
    use crate::slices::delivery::applications::test_support::{World, candidate, service};
    use crate::slices::delivery::domains::delivery::{
        DeliveryError, DeliveryOfferId, OfferState, OrderId, State, TransitionError, Trigger,
    };

    fn handler(world: &World) -> Handler<'_, MemoryStore> {
        Handler {
            authorization: &world.authorization,
            unit_of_work: &world.store,
            deliveries: &world.store,
            couriers: &world.store,
            offers: &world.store,
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

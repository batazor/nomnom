//! Accept Delivery Offer: the addressee accepts a current offer. Exactly one
//! offer of a Delivery becomes the Courier Assignment: the delivery is
//! assigned, the courier becomes busy, the remaining pending offers are
//! withdrawn and the assignment fact is written to the Outbox in one Unit of
//! Work. Before commit `delivery:<id>#courier@user:<courier_id>` is written to
//! the authorization graph; a failed write rolls the acceptance back. A repeated
//! acceptance writes nothing.

use crate::pkg::application::{Clock, Subject, UnitOfWork};
use crate::slices::courier::domains::courier::{CourierId, CourierRepository};
use crate::slices::delivery::domains::delivery::{
    DeliveryId, DeliveryOffer, DeliveryOfferId, DeliveryOfferRepository, DeliveryRepository,
    OfferAcceptanceConditions, OfferState,
};

use super::authorization::{DeliveryAuthorization, DeliveryPermission, DeliveryResource};
use super::courier_locator::CourierLocator;
use super::error::DeliveryApplicationError;
use super::outbox::{DeliveryOutbox, publish};
use super::policy::AssignmentPolicy;
use super::relationships::DeliveryRelationshipWriter;
use super::{courier_id, load_courier, load_delivery, load_offer, offer_id};

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct Input {
    pub offer_id: String,
    pub courier_id: String,
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
    pub deliveries: &'a dyn DeliveryRepository,
    pub couriers: &'a dyn CourierRepository,
    pub outbox: &'a dyn DeliveryOutbox,
    pub locator: &'a dyn CourierLocator,
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
        let offer_id = offer_id(&input.offer_id)?;
        let courier_id = courier_id(&input.courier_id)?;
        self.authorization.check(
            subject,
            DeliveryPermission::AcceptDeliveryOffer,
            &DeliveryResource::Courier(courier_id.clone()),
        )?;

        let offer = load_offer(self.offers, &offer_id)?;
        if is_repeat(&offer, &courier_id) {
            return Ok(output(&offer, true));
        }
        // Values from outside the transaction are gathered before it starts.
        let delivery = load_delivery(self.deliveries, offer.delivery_id())?;
        let snapshot = self
            .locator
            .snapshot(&courier_id, delivery.pickup_address())?
            .ok_or_else(|| DeliveryApplicationError::CourierPositionUnknown(courier_id.clone()))?;
        let conditions = OfferAcceptanceConditions {
            now_epoch_seconds: self.clock.now_epoch_seconds(),
            distance_to_pickup_meters: snapshot.distance_to_pickup_meters,
            maximum_distance_meters: self.policy.maximum_distance_meters,
            active_assignments: snapshot.active_assignments,
            maximum_active_assignments: self.policy.maximum_active_assignments,
        };

        self.unit_of_work
            .run(|| self.accept(&offer_id, &courier_id, conditions))
    }

    fn accept(
        &self,
        offer_id: &DeliveryOfferId,
        courier_id: &CourierId,
        conditions: OfferAcceptanceConditions,
    ) -> Result<Output, DeliveryApplicationError> {
        let mut offer = load_offer(self.offers, offer_id)?;
        if is_repeat(&offer, courier_id) {
            return Ok(output(&offer, true));
        }
        let mut delivery = load_delivery(self.deliveries, offer.delivery_id())?;
        let mut courier = load_courier(self.couriers, courier_id)?;

        offer.accept(courier_id, conditions)?;
        delivery.assign(courier_id.clone())?;
        courier.mark_busy()?;
        // The Courier Assignment is valid; grant access before commit and
        // check the delivery at least as fresh as this write (FR-447).
        let revision = self
            .relationships
            .assign_courier(delivery.id(), courier_id)?;
        delivery.record_authorization_revision(revision);

        for mut other in self.offers.list_for_delivery(delivery.id())? {
            if other.id() != offer.id() && other.state() == OfferState::Pending {
                other.withdraw()?;
                self.offers.save(&other)?;
            }
        }
        self.offers.save(&offer)?;
        self.deliveries.save(&delivery)?;
        self.couriers.save(&courier)?;
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
mod tests {
    use super::*;
    use crate::infrastructure::memory::StaticAuthorization;
    use crate::infrastructure::memory::{RecordingRelationships, RelationshipWrite};
    use crate::pkg::application::UnitOfWorkError;
    use crate::slices::courier::domains::courier::CourierStatus;
    use crate::slices::delivery::applications::authorization::DeliveryResource;
    use crate::slices::delivery::applications::outbox::OutboxMessage;
    use crate::slices::delivery::applications::test_support::{
        NOW, OFFER_TTL, ObservingRelationships, World, candidate, subject,
    };
    use crate::slices::delivery::domains::delivery::{
        DeliveryError, DeliveryOfferError, DeliveryOfferReason, DomainEvent, OfferTrigger, OrderId,
        State, TransitionError,
    };

    fn handler(world: &World) -> Handler<'_, crate::infrastructure::memory::MemoryStore> {
        Handler {
            authorization: &world.authorization,
            unit_of_work: &world.store,
            offers: &world.store,
            deliveries: &world.store,
            couriers: &world.store,
            outbox: &world.store,
            locator: &world.locator,
            clock: &world.clock,
            policy: world.policy,
            relationships: &world.relationships,
        }
    }

    fn input(offer: &str, courier: &str) -> Input {
        Input {
            offer_id: offer.to_owned(),
            courier_id: courier.to_owned(),
        }
    }

    /// Waiting delivery-1 with pending offers to two available couriers.
    fn world_with_two_offers() -> World {
        let world = World::new();
        world.waiting_delivery("delivery-1");
        world.courier("courier-1", CourierStatus::Available);
        world.courier("courier-2", CourierStatus::Available);
        world.offer("offer-1", "delivery-1", "courier-1");
        world.offer("offer-2", "delivery-1", "courier-2");
        world.locator.set_candidates(vec![
            candidate("courier-1", 1_000),
            candidate("courier-2", 2_000),
        ]);
        world
    }

    fn assignment_fact() -> OutboxMessage {
        OutboxMessage::delivery(DomainEvent::CourierAssignedToDelivery {
            delivery_id: DeliveryId::new("delivery-1"),
            order_id: OrderId::new("order-of-delivery-1"),
            courier_id: CourierId::new("courier-1"),
        })
    }

    /// AC-323
    #[test]
    fn accepting_current_offer_creates_assignment_atomically() {
        let world = world_with_two_offers();

        let output = handler(&world)
            .execute(&subject("courier-1"), input("offer-1", "courier-1"))
            .expect("offer accepted");

        assert_eq!(
            output,
            Output {
                offer_id: DeliveryOfferId::new("offer-1"),
                delivery_id: DeliveryId::new("delivery-1"),
                courier_id: CourierId::new("courier-1"),
                already_accepted: false,
            }
        );
        let delivery = world.delivery("delivery-1");
        assert_eq!(delivery.state(), State::Assigned);
        assert_eq!(delivery.courier_id(), Some(&CourierId::new("courier-1")));
        assert_eq!(world.courier_status("courier-1"), CourierStatus::Busy);
        assert_eq!(world.offer_state("offer-1"), OfferState::Accepted);
        assert_eq!(world.offer_state("offer-2"), OfferState::Withdrawn);
        assert_eq!(world.store.outbox_messages(), vec![assignment_fact()]);
        assert_eq!(world.store.commits(), 1);
        assert_eq!(world.relationships.writes(), vec![assigned("courier-1")]);
        // FR-447: the revision of the TOUCH is stored with the delivery.
        let revision = RecordingRelationships::revision(1);
        assert_eq!(delivery.authorization_revision(), Some(&revision));

        // A repeat writes nothing and keeps the stored revision.
        handler(&world)
            .execute(&subject("courier-1"), input("offer-1", "courier-1"))
            .expect("repeat succeeds");
        assert_eq!(world.relationships.writes().len(), 1);
        assert_eq!(
            world.delivery("delivery-1").authorization_revision(),
            Some(&revision)
        );
    }

    fn assigned(courier: &str) -> RelationshipWrite {
        RelationshipWrite::AssignCourier(DeliveryId::new("delivery-1"), CourierId::new(courier))
    }

    /// FR-444: `delivery#courier` is written inside the Unit of Work before the
    /// delivery is saved.
    #[test]
    fn assignment_relationship_is_written_before_delivery_is_saved() {
        let world = world_with_two_offers();
        let writer = ObservingRelationships::new(&world.store);
        let handler = Handler {
            relationships: &writer,
            ..handler(&world)
        };

        handler
            .execute(&subject("courier-1"), input("offer-1", "courier-1"))
            .expect("offer accepted");

        assert_eq!(
            *writer.observed.borrow(),
            vec![(assigned("courier-1"), None)]
        );
        assert_eq!(
            world.delivery("delivery-1").courier_id(),
            Some(&CourierId::new("courier-1"))
        );
    }

    /// FR-444: a failed write rolls the acceptance back (fail closed).
    #[test]
    fn failed_relationship_write_rolls_acceptance_back() {
        let world = World {
            relationships: RecordingRelationships::failing("spicedb offline"),
            ..world_with_two_offers()
        };

        assert!(matches!(
            handler(&world).execute(&subject("courier-1"), input("offer-1", "courier-1")),
            Err(DeliveryApplicationError::DependencyUnavailable(reason)) if reason.contains("spicedb offline")
        ));
        assert_eq!(world.delivery_state("delivery-1"), State::Waiting);
        assert_eq!(world.delivery("delivery-1").courier_id(), None);
        assert_eq!(world.offer_state("offer-1"), OfferState::Pending);
        assert_eq!(world.offer_state("offer-2"), OfferState::Pending);
        assert_eq!(world.courier_status("courier-1"), CourierStatus::Available);
        assert!(world.store.outbox_messages().is_empty());
        assert_eq!(world.store.rollbacks(), 1);
    }

    /// AC-324
    #[test]
    fn second_courier_cannot_accept_already_assigned_delivery() {
        let world = world_with_two_offers();
        handler(&world)
            .execute(&subject("courier-1"), input("offer-1", "courier-1"))
            .expect("first acceptance");

        assert_eq!(
            handler(&world).execute(&subject("courier-2"), input("offer-2", "courier-2")),
            Err(DeliveryApplicationError::OfferPrecondition(
                DeliveryOfferError::InvalidTransition {
                    from: OfferState::Withdrawn,
                    trigger: OfferTrigger::Accept,
                }
            ))
        );
        assert_eq!(
            world.delivery("delivery-1").courier_id(),
            Some(&CourierId::new("courier-1"))
        );
        assert_eq!(world.courier_status("courier-2"), CourierStatus::Available);
        assert_eq!(world.store.outbox_messages(), vec![assignment_fact()]);
        assert_eq!(world.relationships.writes(), vec![assigned("courier-1")]);
    }

    #[test]
    fn stale_pending_offer_of_assigned_delivery_is_rejected_by_delivery() {
        let world = World::new();
        world.assigned_delivery("delivery-1", "courier-1");
        world.courier("courier-2", CourierStatus::Available);
        world.offer("offer-2", "delivery-1", "courier-2");
        world
            .locator
            .set_candidates(vec![candidate("courier-2", 1_000)]);

        assert_eq!(
            handler(&world).execute(&subject("courier-2"), input("offer-2", "courier-2")),
            Err(DeliveryApplicationError::DeliveryPrecondition(
                DeliveryError::Transition(TransitionError::AlreadyInState {
                    state: State::Assigned
                })
            ))
        );
        assert_eq!(world.offer_state("offer-2"), OfferState::Pending);
        assert_eq!(world.courier_status("courier-2"), CourierStatus::Available);
        assert!(world.store.outbox_messages().is_empty());
        assert_eq!(world.store.rollbacks(), 1);
        assert!(world.relationships.writes().is_empty());
    }

    /// AC-325
    #[test]
    fn expired_offer_is_rejected_with_reason() {
        let world = world_with_two_offers();
        world.clock.set(NOW + OFFER_TTL + 1);

        assert_eq!(
            handler(&world).execute(&subject("courier-1"), input("offer-1", "courier-1")),
            Err(DeliveryApplicationError::OfferPrecondition(
                DeliveryOfferError::Unacceptable {
                    reasons: vec![DeliveryOfferReason::Expired]
                }
            ))
        );
        assert_eq!(world.delivery_state("delivery-1"), State::Waiting);
        assert_eq!(world.offer_state("offer-1"), OfferState::Pending);
    }

    /// AC-326
    #[test]
    fn offer_of_another_courier_is_rejected() {
        let world = world_with_two_offers();

        assert_eq!(
            handler(&world).execute(&subject("courier-2"), input("offer-1", "courier-2")),
            Err(DeliveryApplicationError::OfferPrecondition(
                DeliveryOfferError::NotAddressedToCourier
            ))
        );
        assert_eq!(world.offer_state("offer-1"), OfferState::Pending);
        assert_eq!(world.delivery_state("delivery-1"), State::Waiting);
    }

    /// AC-327
    #[test]
    fn repeated_acceptance_is_idempotent_without_second_fact() {
        let world = world_with_two_offers();
        handler(&world)
            .execute(&subject("courier-1"), input("offer-1", "courier-1"))
            .expect("first acceptance");

        let repeat = handler(&world)
            .execute(&subject("courier-1"), input("offer-1", "courier-1"))
            .expect("repeat");

        assert!(repeat.already_accepted);
        assert_eq!(world.store.outbox_messages(), vec![assignment_fact()]);
        assert_eq!(world.store.commits(), 1);
        assert_eq!(world.relationships.writes(), vec![assigned("courier-1")]);
    }

    /// AC-328
    #[test]
    fn failed_commit_persists_nothing() {
        let world = world_with_two_offers();
        world
            .store
            .fail_next_commit(UnitOfWorkError::CommitFailed("connection lost".to_owned()));

        assert_eq!(
            handler(&world).execute(&subject("courier-1"), input("offer-1", "courier-1")),
            Err(DeliveryApplicationError::Transaction(
                UnitOfWorkError::CommitFailed("connection lost".to_owned())
            ))
        );
        assert_eq!(world.offer_state("offer-1"), OfferState::Pending);
        assert_eq!(world.offer_state("offer-2"), OfferState::Pending);
        assert_eq!(world.delivery_state("delivery-1"), State::Waiting);
        assert_eq!(world.courier_status("courier-1"), CourierStatus::Available);
        assert!(world.store.outbox_messages().is_empty());
    }

    /// AC-312
    #[test]
    fn subject_not_acting_for_courier_is_denied_without_changes() {
        let authorization = StaticAuthorization::grants_only();
        authorization.grant_delivery(
            subject("courier-1"),
            DeliveryPermission::AcceptDeliveryOffer,
            DeliveryResource::Courier(CourierId::new("courier-1")),
        );
        let world = World::with_authorization(authorization);
        world.waiting_delivery("delivery-1");
        world.courier("courier-1", CourierStatus::Available);
        world.offer("offer-1", "delivery-1", "courier-1");
        world
            .locator
            .set_candidates(vec![candidate("courier-1", 1_000)]);

        assert_eq!(
            handler(&world).execute(&subject("intruder"), input("offer-1", "courier-1")),
            Err(DeliveryApplicationError::PermissionDenied)
        );
        assert_eq!(world.offer_state("offer-1"), OfferState::Pending);
        assert_eq!(world.delivery_state("delivery-1"), State::Waiting);

        handler(&world)
            .execute(&subject("courier-1"), input("offer-1", "courier-1"))
            .expect("granted courier accepts");
    }

    #[test]
    fn unknown_courier_position_blocks_acceptance() {
        let world = world_with_two_offers();
        world.locator.set_candidates(Vec::new());

        assert_eq!(
            handler(&world).execute(&subject("courier-1"), input("offer-1", "courier-1")),
            Err(DeliveryApplicationError::CourierPositionUnknown(
                CourierId::new("courier-1")
            ))
        );
        assert_eq!(world.offer_state("offer-1"), OfferState::Pending);
    }

    #[test]
    fn unknown_offer_is_not_found() {
        let world = World::new();
        assert_eq!(
            handler(&world).execute(&subject("courier-1"), input("offer-9", "courier-1")),
            Err(DeliveryApplicationError::DeliveryOfferNotFound(
                DeliveryOfferId::new("offer-9")
            ))
        );
    }
}

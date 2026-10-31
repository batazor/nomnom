//! Reassign Courier: the assignment process or an authorized dispatcher ends
//! the Courier Assignment of a not yet picked up Delivery. The delivery
//! returns to waiting, the courier is released and the fact is written to the
//! Outbox. Before commit `delivery:<id>#courier@user:<courier_id>` is deleted
//! from the authorization graph (AC-477); a failed delete rolls the
//! reassignment back. After commit the Assignment Workflow of the delivery is
//! woken to search a courier again (FR-312), and the released courier wakes
//! the search of its Market (FR-706).

use crate::pkg::authz::Subject;
use crate::pkg::unit_of_work::UnitOfWork;
use crate::slices::courier::applications::assignment::CourierAssignment;
use crate::slices::courier::applications::candidate_wake::CandidateWake;
use crate::slices::courier::domains::courier::CourierId;
use crate::slices::delivery::domains::delivery::{Delivery, DeliveryId, DeliveryRepository};

use super::super::assignment_process::{AssignmentProcess, WakeReason};
use super::super::authorization::{DeliveryAuthorization, DeliveryPermission, DeliveryResource};
use super::super::error::DeliveryApplicationError;
use super::super::outbox::{DeliveryOutbox, publish};
use super::super::relationships::DeliveryRelationshipWriter;
use super::super::{delivery_id, load_delivery};
use super::release_assignment;

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct Input {
    pub delivery_id: String,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct Output {
    pub delivery_id: DeliveryId,
    pub released_courier_id: CourierId,
}

pub struct Handler<'a, U: UnitOfWork> {
    pub authorization: &'a dyn DeliveryAuthorization,
    pub unit_of_work: &'a U,
    pub deliveries: &'a dyn DeliveryRepository,
    pub couriers: &'a dyn CourierAssignment,
    pub outbox: &'a dyn DeliveryOutbox,
    pub relationships: &'a dyn DeliveryRelationshipWriter,
    pub assignment: &'a dyn AssignmentProcess,
    pub candidates: &'a dyn CandidateWake,
}

impl<U: UnitOfWork> Handler<'_, U> {
    pub fn execute(
        &self,
        subject: &Subject,
        input: Input,
    ) -> Result<Output, DeliveryApplicationError> {
        let delivery_id = delivery_id(&input.delivery_id)?;
        self.authorization.check(
            subject,
            DeliveryPermission::ReassignCourier,
            &DeliveryResource::Delivery(delivery_id.clone()),
        )?;

        let output = self.unit_of_work.run(|| {
            let mut delivery = load_delivery(self.deliveries, &delivery_id)?;
            let released_courier_id = end_assignment(
                &mut delivery,
                self.deliveries,
                self.couriers,
                self.outbox,
                self.relationships,
            )?;
            Ok::<_, DeliveryApplicationError>(Output {
                delivery_id: delivery_id.clone(),
                released_courier_id,
            })
        })?;
        // The delivery returns to the search, the courier may take another.
        self.assignment
            .wake(&output.delivery_id, WakeReason::CourierUnassigned)?;
        self.candidates
            .courier_available(&output.released_courier_id)?;
        Ok(output)
    }
}

/// Ends the Courier Assignment of a loaded delivery: the delivery returns to
/// waiting, the courier is released, its relationship is revoked and the fact
/// is written to the Outbox. Runs inside the caller's Unit of Work; returns
/// the released courier.
fn end_assignment(
    delivery: &mut Delivery,
    deliveries: &dyn DeliveryRepository,
    couriers: &dyn CourierAssignment,
    outbox: &dyn DeliveryOutbox,
    relationships: &dyn DeliveryRelationshipWriter,
) -> Result<CourierId, DeliveryApplicationError> {
    let assigned = delivery.courier_id().cloned();
    delivery.unassign()?;
    let released_courier_id = assigned.ok_or_else(|| {
        DeliveryApplicationError::Internal("unassigned delivery had no courier".to_owned())
    })?;
    release_assignment(delivery, &released_courier_id, couriers, relationships)?;

    deliveries.save(delivery)?;
    publish(outbox, delivery.pull_events())?;
    Ok(released_courier_id)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::infrastructure::memory::{
        AssignmentCall, RecordingRelationships, RelationshipWrite, StaticAuthorization,
    };
    use crate::infrastructure::repository::test_support::TestStore;
    use crate::slices::courier::domains::courier::CourierStatus;
    use crate::slices::courier::domains::courier::MarketId;
    use crate::slices::delivery::applications::outbox::OutboxMessage;
    use crate::slices::delivery::applications::test_support::{
        ObservingRelationships, World, subject,
    };
    use crate::slices::delivery::domains::delivery::{
        DeliveryError, DomainEvent, OrderId, State, TransitionError, Trigger,
    };

    fn handler(world: &World) -> Handler<'_, TestStore> {
        Handler {
            authorization: &world.authorization,
            unit_of_work: &world.store,
            deliveries: &world.store,
            couriers: &world.store,
            outbox: &world.store,
            relationships: &world.relationships,
            assignment: &world.assignment,
            candidates: &world.assignment,
        }
    }

    fn input() -> Input {
        Input {
            delivery_id: "delivery-1".to_owned(),
        }
    }

    /// AC-331
    #[test]
    fn reassignment_returns_delivery_to_waiting_and_releases_courier() {
        let world = World::new();
        world.assigned_delivery("delivery-1", "courier-1");

        let output = handler(&world)
            .execute(&subject("dispatcher-1"), input())
            .expect("reassigned");

        assert_eq!(output.released_courier_id, CourierId::new("courier-1"));
        let delivery = world.delivery("delivery-1");
        assert_eq!(delivery.state(), State::Waiting);
        assert_eq!(delivery.courier_id(), None);
        assert_eq!(world.courier_status("courier-1"), CourierStatus::Available);
        assert_eq!(
            world.store.outbox_messages(),
            vec![OutboxMessage::delivery(
                DomainEvent::CourierUnassignedFromDelivery {
                    delivery_id: DeliveryId::new("delivery-1"),
                    order_id: OrderId::new("order-of-delivery-1"),
                    courier_id: CourierId::new("courier-1"),
                    market_id: MarketId::new("berlin").expect("market id"),
                }
            )]
        );
        assert_eq!(world.relationships.writes(), vec![unassigned()]);
        // FR-447, AC-483: the revision of the DELETE is stored with the delivery.
        assert_eq!(
            delivery.authorization_revision(),
            Some(&RecordingRelationships::revision(1))
        );
    }

    fn unassigned() -> RelationshipWrite {
        RelationshipWrite::UnassignCourier(
            DeliveryId::new("delivery-1"),
            CourierId::new("courier-1"),
        )
    }

    /// AC-477: the relationship is deleted before the delivery is saved.
    #[test]
    fn courier_relationship_is_deleted_before_delivery_is_saved() {
        let world = World::new();
        world.assigned_delivery("delivery-1", "courier-1");
        let writer = ObservingRelationships::new(&world.store);
        let handler = Handler {
            relationships: &writer,
            ..handler(&world)
        };

        handler
            .execute(&subject("dispatcher-1"), input())
            .expect("reassigned");

        assert_eq!(
            *writer.observed.borrow(),
            vec![(unassigned(), Some(CourierId::new("courier-1")))]
        );
        assert_eq!(world.delivery("delivery-1").courier_id(), None);
    }

    /// FR-444: a failed delete rolls the reassignment back (fail closed).
    #[test]
    fn failed_relationship_delete_rolls_reassignment_back() {
        let world = World {
            relationships: RecordingRelationships::failing("spicedb offline"),
            ..World::new()
        };
        world.assigned_delivery("delivery-1", "courier-1");

        assert!(matches!(
            handler(&world).execute(&subject("dispatcher-1"), input()),
            Err(DeliveryApplicationError::DependencyUnavailable(_))
        ));
        assert_eq!(world.delivery_state("delivery-1"), State::Assigned);
        assert_eq!(
            world.delivery("delivery-1").courier_id(),
            Some(&CourierId::new("courier-1"))
        );
        assert_eq!(world.courier_status("courier-1"), CourierStatus::Busy);
        assert!(world.store.outbox_messages().is_empty());
    }

    /// AC-332
    #[test]
    fn picked_up_delivery_cannot_be_reassigned() {
        let world = World::new();
        world.picked_up_delivery("delivery-1", "courier-1");

        assert_eq!(
            handler(&world).execute(&subject("dispatcher-1"), input()),
            Err(DeliveryApplicationError::DeliveryPrecondition(
                DeliveryError::Transition(TransitionError::InvalidTransition {
                    from: State::PickedUp,
                    trigger: Trigger::Unassign,
                })
            ))
        );
        assert_eq!(world.delivery_state("delivery-1"), State::PickedUp);
        assert_eq!(world.courier_status("courier-1"), CourierStatus::Busy);
        assert!(world.store.outbox_messages().is_empty());
        assert!(world.relationships.writes().is_empty());
    }

    #[test]
    fn denied_subject_cannot_reassign() {
        let world = World::with_authorization(StaticAuthorization::deny_all());
        world.assigned_delivery("delivery-1", "courier-1");
        assert_eq!(
            handler(&world).execute(&subject("courier-2"), input()),
            Err(DeliveryApplicationError::PermissionDenied)
        );
        assert_eq!(world.delivery_state("delivery-1"), State::Assigned);
    }

    /// FR-312, FR-706: the delivery returns to the search and the released
    /// courier wakes the search of its Market.
    #[test]
    fn reassignment_wakes_the_delivery_and_the_courier_search() {
        let world = World::new();
        world.assigned_delivery("delivery-1", "courier-1");

        handler(&world)
            .execute(&subject("dispatcher-1"), input())
            .expect("reassigned");

        assert_eq!(
            world.assignment.calls(),
            vec![
                AssignmentCall::Wake(DeliveryId::new("delivery-1"), WakeReason::CourierUnassigned),
                AssignmentCall::CourierAvailable(CourierId::new("courier-1")),
            ]
        );
    }
}

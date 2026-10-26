//! Confirm Delivery: the assigned courier confirms the handover to the
//! customer. The delivery becomes delivered, the courier available again and
//! the Delivery Completion fact is written to the Outbox in one Unit of Work.
//! The assignment is over, so `delivery:<id>#courier@user:<courier_id>` is
//! deleted before commit (AC-690); `Delivery.courier_id` stays as the fact of
//! who carried it. A failed delete rolls the completion back (AC-692).

use crate::pkg::application::{Subject, UnitOfWork};
use crate::slices::courier::applications::assignment::CourierAssignment;
use crate::slices::delivery::domains::delivery::{
    DeliveryError, DeliveryId, DeliveryRepository, State, TransitionError,
};

use super::assignment::release_assignment;
use super::authorization::{DeliveryAuthorization, DeliveryPermission, DeliveryResource};
use super::error::DeliveryApplicationError;
use super::outbox::{DeliveryOutbox, publish};
use super::relationships::DeliveryRelationshipWriter;
use super::{courier_id, delivery_id, load_delivery};

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct Input {
    pub delivery_id: String,
    pub courier_id: String,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct Output {
    pub delivery_id: DeliveryId,
    pub state: State,
    /// `false` when the same courier had already confirmed the completion.
    pub changed: bool,
}

pub struct Handler<'a, U: UnitOfWork> {
    pub authorization: &'a dyn DeliveryAuthorization,
    pub unit_of_work: &'a U,
    pub deliveries: &'a dyn DeliveryRepository,
    pub couriers: &'a dyn CourierAssignment,
    pub outbox: &'a dyn DeliveryOutbox,
    pub relationships: &'a dyn DeliveryRelationshipWriter,
}

impl<U: UnitOfWork> Handler<'_, U> {
    pub fn execute(
        &self,
        subject: &Subject,
        input: Input,
    ) -> Result<Output, DeliveryApplicationError> {
        let delivery_id = delivery_id(&input.delivery_id)?;
        let courier_id = courier_id(&input.courier_id)?;
        self.authorization.check(
            subject,
            DeliveryPermission::ConfirmDelivery,
            &DeliveryResource::Courier(courier_id.clone()),
        )?;

        self.unit_of_work.run(|| {
            let mut delivery = load_delivery(self.deliveries, &delivery_id)?;
            match delivery.complete(&courier_id) {
                Ok(_) => {}
                Err(DeliveryError::Transition(TransitionError::AlreadyInState { .. }))
                    if delivery.courier_id() == Some(&courier_id) =>
                {
                    return Ok(Output {
                        delivery_id: delivery_id.clone(),
                        state: delivery.state(),
                        changed: false,
                    });
                }
                Err(error) => return Err(error.into()),
            }
            release_assignment(
                &mut delivery,
                &courier_id,
                self.couriers,
                self.relationships,
            )?;

            self.deliveries.save(&delivery)?;
            publish(self.outbox, delivery.pull_events())?;
            Ok(Output {
                delivery_id: delivery_id.clone(),
                state: delivery.state(),
                changed: true,
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
    use crate::infrastructure::repository::test_support::TestStore;
    use crate::pkg::application::UnitOfWorkError;
    use crate::slices::courier::domains::courier::MarketId;
    use crate::slices::courier::domains::courier::{CourierId, CourierStatus};
    use crate::slices::delivery::applications::outbox::OutboxMessage;
    use crate::slices::delivery::applications::test_support::{World, subject};
    use crate::slices::delivery::domains::delivery::{DomainEvent, OrderId, Trigger};

    fn handler(world: &World) -> Handler<'_, TestStore> {
        Handler {
            authorization: &world.authorization,
            unit_of_work: &world.store,
            deliveries: &world.store,
            couriers: &world.store,
            outbox: &world.store,
            relationships: &world.relationships,
        }
    }

    fn input(courier: &str) -> Input {
        Input {
            delivery_id: "delivery-1".to_owned(),
            courier_id: courier.to_owned(),
        }
    }

    /// AC-338
    #[test]
    fn completion_releases_courier_and_records_fact() {
        let world = World::new();
        world.picked_up_delivery("delivery-1", "courier-1");

        let output = handler(&world)
            .execute(&subject("courier-1"), input("courier-1"))
            .expect("delivery confirmed");

        assert_eq!(output.state, State::Delivered);
        assert_eq!(world.delivery_state("delivery-1"), State::Delivered);
        assert_eq!(world.courier_status("courier-1"), CourierStatus::Available);
        assert_eq!(
            world.store.outbox_messages(),
            vec![OutboxMessage::delivery(DomainEvent::DeliveryCompleted {
                delivery_id: DeliveryId::new("delivery-1"),
                order_id: OrderId::new("order-of-delivery-1"),
                courier_id: CourierId::new("courier-1"),
                created_at_epoch_seconds: None,
                market_id: MarketId::new("berlin").expect("market id"),
            })]
        );
    }

    /// AC-690: the courier loses access, the delivery keeps who carried it.
    #[test]
    fn completion_revokes_courier_relationship() {
        let world = World::new();
        world.picked_up_delivery("delivery-1", "courier-1");

        handler(&world)
            .execute(&subject("courier-1"), input("courier-1"))
            .expect("delivery confirmed");

        assert_eq!(
            world.relationships.writes(),
            vec![RelationshipWrite::UnassignCourier(
                DeliveryId::new("delivery-1"),
                CourierId::new("courier-1")
            )]
        );
        let delivery = world.delivery("delivery-1");
        assert_eq!(delivery.courier_id(), Some(&CourierId::new("courier-1")));
        assert_eq!(delivery.active_courier(), None);
        // FR-447: the revision of the DELETE is stored with the delivery.
        assert_eq!(
            delivery.authorization_revision(),
            Some(&RecordingRelationships::revision(1))
        );
    }

    /// AC-692: a failed DELETE rolls the completion back.
    #[test]
    fn failed_relationship_delete_rolls_completion_back() {
        let world = World {
            relationships: RecordingRelationships::failing("spicedb offline"),
            ..World::new()
        };
        world.picked_up_delivery("delivery-1", "courier-1");

        assert!(matches!(
            handler(&world).execute(&subject("courier-1"), input("courier-1")),
            Err(DeliveryApplicationError::DependencyUnavailable(_))
        ));
        assert_eq!(world.delivery_state("delivery-1"), State::PickedUp);
        assert_eq!(world.courier_status("courier-1"), CourierStatus::Busy);
        assert!(world.store.outbox_messages().is_empty());
    }

    /// AC-339
    #[test]
    fn completion_before_pickup_is_rejected() {
        let world = World::new();
        world.assigned_delivery("delivery-1", "courier-1");

        assert_eq!(
            handler(&world).execute(&subject("courier-1"), input("courier-1")),
            Err(DeliveryApplicationError::DeliveryPrecondition(
                DeliveryError::Transition(TransitionError::InvalidTransition {
                    from: State::Assigned,
                    trigger: Trigger::Complete,
                })
            ))
        );
        assert_eq!(world.delivery_state("delivery-1"), State::Assigned);
        assert_eq!(world.courier_status("courier-1"), CourierStatus::Busy);
        assert!(world.store.outbox_messages().is_empty());
    }

    /// AC-343
    #[test]
    fn failed_commit_keeps_pickup_state_and_empty_outbox() {
        let world = World::new();
        world.picked_up_delivery("delivery-1", "courier-1");
        world
            .store
            .fail_next_commit(UnitOfWorkError::CommitOutcomeUnknown);

        assert_eq!(
            handler(&world).execute(&subject("courier-1"), input("courier-1")),
            Err(DeliveryApplicationError::Transaction(
                UnitOfWorkError::CommitOutcomeUnknown
            ))
        );
        assert_eq!(world.delivery_state("delivery-1"), State::PickedUp);
        assert_eq!(world.courier_status("courier-1"), CourierStatus::Busy);
        assert!(world.store.outbox_messages().is_empty());
    }

    #[test]
    fn repeated_completion_is_a_no_op_without_second_fact() {
        let world = World::new();
        world.picked_up_delivery("delivery-1", "courier-1");
        handler(&world)
            .execute(&subject("courier-1"), input("courier-1"))
            .expect("first");

        let repeat = handler(&world)
            .execute(&subject("courier-1"), input("courier-1"))
            .expect("repeat");
        assert!(!repeat.changed);
        assert_eq!(world.store.outbox_messages().len(), 1);
        // The repeat writes nothing: the relationship was deleted once.
        assert_eq!(world.relationships.writes().len(), 1);
    }

    #[test]
    fn other_courier_cannot_complete() {
        let world = World::new();
        world.picked_up_delivery("delivery-1", "courier-1");
        assert_eq!(
            handler(&world).execute(&subject("courier-2"), input("courier-2")),
            Err(DeliveryApplicationError::DeliveryPrecondition(
                DeliveryError::CourierNotAssigned
            ))
        );
        assert_eq!(world.delivery_state("delivery-1"), State::PickedUp);
    }

    #[test]
    fn denied_subject_cannot_complete() {
        let world = World::with_authorization(StaticAuthorization::deny_all());
        world.picked_up_delivery("delivery-1", "courier-1");
        assert_eq!(
            handler(&world).execute(&subject("courier-1"), input("courier-1")),
            Err(DeliveryApplicationError::PermissionDenied)
        );
        assert_eq!(world.delivery_state("delivery-1"), State::PickedUp);
    }
}

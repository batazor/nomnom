//! Confirm Pickup: the assigned courier confirms that the order was picked up
//! at the restaurant and may name the expected handover time; a named one
//! must be in the future (FR-610). The Pickup fact with that time is written
//! to the Outbox in the same Unit of Work. After commit the Pickup ends the
//! Assignment Workflow of the delivery (FR-704).

use crate::pkg::authz::Subject;
use crate::pkg::unit_of_work::UnitOfWork;
use crate::pkg::usecase::Clock;
use crate::slices::delivery::domains::delivery::{
    DeliveryError, DeliveryId, DeliveryRepository, State, TransitionError,
};

use super::assignment_process::{AssignmentProcess, WakeReason};
use super::authorization::{DeliveryAuthorization, DeliveryPermission, DeliveryResource};
use super::error::DeliveryApplicationError;
use super::outbox::{DeliveryOutbox, publish};
use super::{courier_id, delivery_id, expected_time, load_delivery};

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct Input {
    pub delivery_id: String,
    pub courier_id: String,
    /// Handover time the courier names; `None` — not named.
    pub expected_delivered_at_epoch_seconds: Option<i64>,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct Output {
    pub delivery_id: DeliveryId,
    pub state: State,
    /// `false` when the same courier had already confirmed the Pickup.
    pub changed: bool,
}

pub struct Handler<'a, U: UnitOfWork> {
    pub authorization: &'a dyn DeliveryAuthorization,
    pub unit_of_work: &'a U,
    pub deliveries: &'a dyn DeliveryRepository,
    pub outbox: &'a dyn DeliveryOutbox,
    pub clock: &'a dyn Clock,
    pub assignment: &'a dyn AssignmentProcess,
}

impl<U: UnitOfWork> Handler<'_, U> {
    pub fn execute(
        &self,
        subject: &Subject,
        input: Input,
    ) -> Result<Output, DeliveryApplicationError> {
        let delivery_id = delivery_id(&input.delivery_id)?;
        let courier_id = courier_id(&input.courier_id)?;
        let expected_delivered_at = expected_time(
            "expected_delivered_at",
            input.expected_delivered_at_epoch_seconds,
            self.clock,
        )?;
        self.authorization.check(
            subject,
            DeliveryPermission::ConfirmPickup,
            &DeliveryResource::Courier(courier_id.clone()),
        )?;

        let output = self.unit_of_work.run(|| {
            let mut delivery = load_delivery(self.deliveries, &delivery_id)?;
            let changed = match delivery.confirm_pickup(&courier_id, expected_delivered_at) {
                Ok(_) => true,
                Err(DeliveryError::Transition(TransitionError::AlreadyInState { .. }))
                    if delivery.courier_id() == Some(&courier_id) =>
                {
                    false
                }
                Err(error) => return Err(error.into()),
            };
            if changed {
                self.deliveries.save(&delivery)?;
                publish(self.outbox, delivery.pull_events())?;
            }
            Ok::<_, DeliveryApplicationError>(Output {
                delivery_id: delivery_id.clone(),
                state: delivery.state(),
                changed,
            })
        })?;
        // Pickup ends the Assignment Workflow (FR-704); also on a repeat.
        self.assignment
            .wake(&output.delivery_id, WakeReason::DeliveryClosed)?;
        Ok(output)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::infrastructure::memory::StaticAuthorization;
    use crate::infrastructure::repository::test_support::TestStore;
    use crate::slices::courier::domains::courier::CourierId;
    use crate::slices::courier::domains::courier::MarketId;
    use crate::slices::delivery::applications::authorization::DeliveryResource;
    use crate::slices::delivery::applications::error::InvalidDeliveryRequest;
    use crate::slices::delivery::applications::outbox::OutboxMessage;
    use crate::slices::delivery::applications::test_support::{NOW, World, subject};
    use crate::slices::delivery::domains::delivery::{
        DeliveryValidationError, DomainEvent, ExpectedTime, OrderId,
    };

    fn handler(world: &World) -> Handler<'_, TestStore> {
        Handler {
            authorization: &world.authorization,
            unit_of_work: &world.store,
            deliveries: &world.store,
            outbox: &world.store,
            clock: &world.clock,
            assignment: &world.assignment,
        }
    }

    fn input(courier: &str) -> Input {
        Input {
            delivery_id: "delivery-1".to_owned(),
            courier_id: courier.to_owned(),
            expected_delivered_at_epoch_seconds: None,
        }
    }

    fn input_expecting(courier: &str, expected_delivered_at: i64) -> Input {
        Input {
            expected_delivered_at_epoch_seconds: Some(expected_delivered_at),
            ..input(courier)
        }
    }

    /// AC-336
    #[test]
    fn assigned_courier_confirms_pickup_and_fact_is_recorded() {
        let world = World::new();
        world.assigned_delivery("delivery-1", "courier-1");

        let output = handler(&world)
            .execute(&subject("courier-1"), input("courier-1"))
            .expect("pickup confirmed");

        assert_eq!(output.state, State::PickedUp);
        assert!(output.changed);
        assert_eq!(world.delivery_state("delivery-1"), State::PickedUp);
        assert_eq!(
            world.store.outbox_messages(),
            vec![OutboxMessage::delivery(DomainEvent::DeliveryPickedUp {
                delivery_id: DeliveryId::new("delivery-1"),
                order_id: OrderId::new("order-of-delivery-1"),
                courier_id: CourierId::new("courier-1"),
                expected_delivered_at_epoch_seconds: None,
                market_id: MarketId::new("berlin").expect("market id"),
            })]
        );
    }

    /// AC-770: the handover time named on Pickup is stored and reaches
    /// `DeliveryPickedUp`.
    #[test]
    fn pickup_with_expected_handover_time_records_it() {
        let world = World::new();
        world.assigned_delivery("delivery-1", "courier-1");

        handler(&world)
            .execute(
                &subject("courier-1"),
                input_expecting("courier-1", NOW + 900),
            )
            .expect("pickup confirmed");

        assert_eq!(
            world.delivery("delivery-1").expected_delivered_at(),
            Some(ExpectedTime::restore(NOW + 900))
        );
        assert_eq!(
            world.store.outbox_messages(),
            vec![OutboxMessage::delivery(DomainEvent::DeliveryPickedUp {
                delivery_id: DeliveryId::new("delivery-1"),
                order_id: OrderId::new("order-of-delivery-1"),
                courier_id: CourierId::new("courier-1"),
                expected_delivered_at_epoch_seconds: Some(NOW + 900),
                market_id: MarketId::new("berlin").expect("market id"),
            })]
        );
    }

    /// FR-610: a named handover time not in the future is an invalid request.
    #[test]
    fn handover_time_not_in_the_future_is_rejected() {
        let world = World::new();
        world.assigned_delivery("delivery-1", "courier-1");

        for at in [NOW, NOW - 1] {
            assert_eq!(
                handler(&world).execute(&subject("courier-1"), input_expecting("courier-1", at)),
                Err(DeliveryApplicationError::InvalidRequest(
                    InvalidDeliveryRequest::Validation(
                        DeliveryValidationError::ExpectedTimeNotInFuture {
                            field: "expected_delivered_at"
                        }
                    )
                ))
            );
        }
        assert_eq!(world.delivery_state("delivery-1"), State::Assigned);
        assert!(world.store.outbox_messages().is_empty());
    }

    /// AC-337
    #[test]
    fn other_courier_cannot_confirm_pickup() {
        let world = World::new();
        world.assigned_delivery("delivery-1", "courier-1");

        assert_eq!(
            handler(&world).execute(&subject("courier-2"), input("courier-2")),
            Err(DeliveryApplicationError::DeliveryPrecondition(
                DeliveryError::CourierNotAssigned
            ))
        );
        assert_eq!(world.delivery_state("delivery-1"), State::Assigned);
        assert!(world.store.outbox_messages().is_empty());
    }

    #[test]
    fn repeated_pickup_by_assigned_courier_is_a_no_op() {
        let world = World::new();
        world.picked_up_delivery("delivery-1", "courier-1");

        let output = handler(&world)
            .execute(&subject("courier-1"), input("courier-1"))
            .expect("repeat");
        assert!(!output.changed);
        assert!(world.store.outbox_messages().is_empty());
    }

    /// AC-335
    #[test]
    fn subject_not_acting_for_assigned_courier_is_denied() {
        let authorization = StaticAuthorization::grants_only();
        authorization.grant_delivery(
            subject("courier-1"),
            DeliveryPermission::ConfirmPickup,
            DeliveryResource::Courier(CourierId::new("courier-1")),
        );
        let world = World::with_authorization(authorization);
        world.assigned_delivery("delivery-1", "courier-1");

        assert_eq!(
            handler(&world).execute(&subject("courier-2"), input("courier-1")),
            Err(DeliveryApplicationError::PermissionDenied)
        );
        assert_eq!(world.delivery_state("delivery-1"), State::Assigned);
        assert!(world.store.outbox_messages().is_empty());
    }
}

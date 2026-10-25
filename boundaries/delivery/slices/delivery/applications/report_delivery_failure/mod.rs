//! Report Delivery Failure: the assigned courier or an authorized dispatcher
//! records that an assigned or picked up Delivery cannot be completed. The
//! reason is stored and published as is; whether it is retryable is decided
//! outside `delivery`.

use crate::pkg::application::{Subject, UnitOfWork};
use crate::slices::courier::applications::assignment::CourierAssignment;
use crate::slices::courier::domains::courier::CourierId;
use crate::slices::delivery::domains::delivery::{
    DeliveryFailureReason, DeliveryId, DeliveryRepository, FailureReporter,
};

use super::assignment::{Revoke, release_assignment};
use super::authorization::{DeliveryAuthorization, DeliveryPermission, DeliveryResource};
use super::error::DeliveryApplicationError;
use super::outbox::{DeliveryOutbox, publish};
use super::{courier_id, delivery_id, load_delivery};

#[derive(Clone, Debug, Eq, PartialEq)]
pub enum Reporter {
    /// Identifier of the courier reporting on its own assignment.
    Courier(String),
    Dispatcher,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct Input {
    pub delivery_id: String,
    pub reporter: Reporter,
    pub reason: String,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct Output {
    pub delivery_id: DeliveryId,
    pub reason: DeliveryFailureReason,
    pub released_courier_id: CourierId,
}

pub struct Handler<'a, U: UnitOfWork> {
    pub authorization: &'a dyn DeliveryAuthorization,
    pub unit_of_work: &'a U,
    pub deliveries: &'a dyn DeliveryRepository,
    pub couriers: &'a dyn CourierAssignment,
    pub outbox: &'a dyn DeliveryOutbox,
}

impl<U: UnitOfWork> Handler<'_, U> {
    pub fn execute(
        &self,
        subject: &Subject,
        input: Input,
    ) -> Result<Output, DeliveryApplicationError> {
        let delivery_id = delivery_id(&input.delivery_id)?;
        let reason = DeliveryFailureReason::new(&input.reason)?;
        let (reporter, resource) = match &input.reporter {
            Reporter::Courier(raw) => {
                let courier_id = courier_id(raw)?;
                (
                    FailureReporter::AssignedCourier(courier_id.clone()),
                    DeliveryResource::Courier(courier_id),
                )
            }
            Reporter::Dispatcher => (
                FailureReporter::Dispatcher,
                DeliveryResource::Delivery(delivery_id.clone()),
            ),
        };
        self.authorization.check(
            subject,
            DeliveryPermission::ReportDeliveryFailure,
            &resource,
        )?;

        self.unit_of_work.run(|| {
            let mut delivery = load_delivery(self.deliveries, &delivery_id)?;
            delivery.fail(reason.clone(), &reporter)?;
            let released_courier_id = delivery.courier_id().cloned().ok_or_else(|| {
                DeliveryApplicationError::Internal("failed delivery had no courier".to_owned())
            })?;
            // The failed delivery keeps its courier, so does the
            // relationship (FR-444).
            release_assignment(
                &mut delivery,
                &released_courier_id,
                self.couriers,
                Revoke::Keep,
            )?;

            self.deliveries.save(&delivery)?;
            publish(self.outbox, delivery.pull_events())?;
            Ok(Output {
                delivery_id: delivery_id.clone(),
                reason: reason.clone(),
                released_courier_id,
            })
        })
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::infrastructure::memory::{MemoryStore, StaticAuthorization};
    use crate::slices::courier::domains::courier::CourierStatus;
    use crate::slices::delivery::applications::error::InvalidDeliveryRequest;
    use crate::slices::delivery::applications::outbox::OutboxMessage;
    use crate::slices::delivery::applications::test_support::{World, subject};
    use crate::slices::delivery::domains::delivery::{
        DeliveryError, DeliveryValidationError, DomainEvent, OrderId, State, TransitionError,
        Trigger,
    };

    fn handler(world: &World) -> Handler<'_, MemoryStore> {
        Handler {
            authorization: &world.authorization,
            unit_of_work: &world.store,
            deliveries: &world.store,
            couriers: &world.store,
            outbox: &world.store,
        }
    }

    fn input(reporter: Reporter, reason: &str) -> Input {
        Input {
            delivery_id: "delivery-1".to_owned(),
            reporter,
            reason: reason.to_owned(),
        }
    }

    fn reason(value: &str) -> DeliveryFailureReason {
        DeliveryFailureReason::new(value).expect("reason")
    }

    /// AC-340
    #[test]
    fn courier_reports_failure_of_picked_up_delivery() {
        let world = World::new();
        world.picked_up_delivery("delivery-1", "courier-1");

        let output = handler(&world)
            .execute(
                &subject("courier-1"),
                input(
                    Reporter::Courier("courier-1".to_owned()),
                    " customer  unreachable ",
                ),
            )
            .expect("failure reported");

        assert_eq!(output.reason, reason("customer unreachable"));
        let delivery = world.delivery("delivery-1");
        assert_eq!(delivery.state(), State::Failed);
        assert_eq!(
            delivery.failure_reason(),
            Some(&reason("customer unreachable"))
        );
        assert_eq!(world.courier_status("courier-1"), CourierStatus::Available);
        assert_eq!(
            world.store.outbox_messages(),
            vec![OutboxMessage::delivery(DomainEvent::DeliveryFailed {
                delivery_id: DeliveryId::new("delivery-1"),
                order_id: OrderId::new("order-of-delivery-1"),
                courier_id: CourierId::new("courier-1"),
                reason: reason("customer unreachable"),
            })]
        );
        assert_eq!(
            world.authorization.delivery_checks()[0].2,
            DeliveryResource::Courier(CourierId::new("courier-1"))
        );
    }

    /// AC-341
    #[test]
    fn dispatcher_reports_failure_of_assigned_delivery() {
        let world = World::new();
        world.assigned_delivery("delivery-1", "courier-1");

        let output = handler(&world)
            .execute(
                &subject("dispatcher-1"),
                input(Reporter::Dispatcher, "restaurant closed"),
            )
            .expect("failure reported");

        assert_eq!(output.released_courier_id, CourierId::new("courier-1"));
        assert_eq!(world.delivery_state("delivery-1"), State::Failed);
        assert_eq!(world.courier_status("courier-1"), CourierStatus::Available);
        assert_eq!(world.store.outbox_messages().len(), 1);
        assert_eq!(
            world.authorization.delivery_checks()[0].2,
            DeliveryResource::Delivery(DeliveryId::new("delivery-1"))
        );
    }

    /// AC-342
    #[test]
    fn delivered_delivery_cannot_fail() {
        let world = World::new();
        world.picked_up_delivery("delivery-1", "courier-1");
        let mut delivered = world.delivery("delivery-1");
        delivered
            .complete(&CourierId::new("courier-1"))
            .expect("complete");
        DeliveryRepository::save(&world.store, &delivered).expect("save");

        assert_eq!(
            handler(&world).execute(
                &subject("dispatcher-1"),
                input(Reporter::Dispatcher, "late report")
            ),
            Err(DeliveryApplicationError::DeliveryPrecondition(
                DeliveryError::Transition(TransitionError::InvalidTransition {
                    from: State::Delivered,
                    trigger: Trigger::Fail,
                })
            ))
        );
        assert_eq!(world.delivery_state("delivery-1"), State::Delivered);
        assert!(world.store.outbox_messages().is_empty());
    }

    #[test]
    fn other_courier_cannot_report_failure() {
        let world = World::new();
        world.assigned_delivery("delivery-1", "courier-1");
        assert_eq!(
            handler(&world).execute(
                &subject("courier-2"),
                input(Reporter::Courier("courier-2".to_owned()), "flat tyre")
            ),
            Err(DeliveryApplicationError::DeliveryPrecondition(
                DeliveryError::CourierNotAssigned
            ))
        );
        assert_eq!(world.delivery_state("delivery-1"), State::Assigned);
    }

    #[test]
    fn empty_reason_is_invalid() {
        let world = World::new();
        world.assigned_delivery("delivery-1", "courier-1");
        assert_eq!(
            handler(&world).execute(&subject("dispatcher-1"), input(Reporter::Dispatcher, " ")),
            Err(DeliveryApplicationError::InvalidRequest(
                InvalidDeliveryRequest::Validation(DeliveryValidationError::EmptyFailureReason)
            ))
        );
    }

    #[test]
    fn denied_subject_cannot_report_failure() {
        let world = World::with_authorization(StaticAuthorization::deny_all());
        world.assigned_delivery("delivery-1", "courier-1");
        assert_eq!(
            handler(&world).execute(
                &subject("intruder"),
                input(Reporter::Dispatcher, "restaurant closed")
            ),
            Err(DeliveryApplicationError::PermissionDenied)
        );
        assert_eq!(world.delivery_state("delivery-1"), State::Assigned);
    }
}

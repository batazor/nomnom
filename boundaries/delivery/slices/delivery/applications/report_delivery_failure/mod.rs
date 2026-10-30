//! Report Delivery Failure: the assigned courier or an authorized dispatcher
//! records that an assigned or picked up Delivery cannot be completed. The
//! reason is a code of the Delivery Failure Reason catalog plus an optional
//! free-text detail; an unknown code is invalid input. Whether a code is
//! retryable is decided outside `delivery`.
//!
//! The assignment is over, so `delivery:<id>#courier@user:<courier_id>` is
//! deleted before commit (AC-691); a failed delete rolls the failure back.
//! The dispatcher is authorized by the platform right `dispatch_delivery`
//! (AC-694), mapped from the `Delivery(id)` resource. After commit a failure
//! before Pickup ends the Assignment Workflow, and the released courier wakes
//! the courier search of its Market (FR-706).

use crate::pkg::authz::Subject;
use crate::pkg::unit_of_work::UnitOfWork;
use crate::slices::courier::applications::assignment::CourierAssignment;
use crate::slices::courier::applications::candidate_wake::CandidateWake;
use crate::slices::courier::domains::courier::CourierId;
use crate::slices::delivery::domains::delivery::{
    DeliveryFailureReason, DeliveryId, DeliveryRepository, FailureReporter,
};

use super::assignment::release_assignment;
use super::assignment_process::{AssignmentProcess, WakeReason};
use super::authorization::{DeliveryAuthorization, DeliveryPermission, DeliveryResource};
use super::error::DeliveryApplicationError;
use super::outbox::{DeliveryOutbox, publish};
use super::relationships::DeliveryRelationshipWriter;
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
    /// Code of the Delivery Failure Reason catalog, e.g. `customer_unreachable`.
    pub reason: String,
    /// Optional free text; blank means no detail.
    pub detail: Option<String>,
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
        let reason = DeliveryFailureReason::new(&input.reason, input.detail.as_deref())?;
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

        let output = self
            .unit_of_work
            .run(|| -> Result<Output, DeliveryApplicationError> {
                let mut delivery = load_delivery(self.deliveries, &delivery_id)?;
                delivery.fail(reason.clone(), &reporter)?;
                let released_courier_id = delivery.courier_id().cloned().ok_or_else(|| {
                    DeliveryApplicationError::Internal("failed delivery had no courier".to_owned())
                })?;
                release_assignment(
                    &mut delivery,
                    &released_courier_id,
                    self.couriers,
                    self.relationships,
                )?;

                self.deliveries.save(&delivery)?;
                publish(self.outbox, delivery.pull_events())?;
                Ok(Output {
                    delivery_id: delivery_id.clone(),
                    reason: reason.clone(),
                    released_courier_id,
                })
            })?;
        // A failure before Pickup ends the Assignment Workflow; the released
        // courier may take a waiting delivery (FR-706).
        self.assignment
            .wake(&output.delivery_id, WakeReason::DeliveryClosed)?;
        self.candidates
            .courier_available(&output.released_courier_id)?;
        Ok(output)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::infrastructure::memory::{
        RecordingRelationships, RelationshipWrite, StaticAuthorization,
    };
    use crate::infrastructure::repository::test_support::TestStore;
    use crate::slices::courier::domains::courier::CourierStatus;
    use crate::slices::courier::domains::courier::MarketId;
    use crate::slices::delivery::applications::error::InvalidDeliveryRequest;
    use crate::slices::delivery::applications::outbox::OutboxMessage;
    use crate::slices::delivery::applications::test_support::{World, subject};
    use crate::slices::delivery::domains::delivery::{
        DeliveryError, DeliveryValidationError, DomainEvent, OrderId, State, TransitionError,
        Trigger,
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

    fn input(reporter: Reporter, reason: &str) -> Input {
        Input {
            delivery_id: "delivery-1".to_owned(),
            reporter,
            reason: reason.to_owned(),
            detail: None,
        }
    }

    fn reason(code: &str, detail: Option<&str>) -> DeliveryFailureReason {
        DeliveryFailureReason::new(code, detail).expect("reason")
    }

    /// AC-340
    #[test]
    fn courier_reports_failure_of_picked_up_delivery() {
        let world = World::new();
        world.picked_up_delivery("delivery-1", "courier-1");

        let output = handler(&world)
            .execute(
                &subject("courier-1"),
                Input {
                    detail: Some(" rang   twice ".to_owned()),
                    ..input(
                        Reporter::Courier("courier-1".to_owned()),
                        " customer_unreachable ",
                    )
                },
            )
            .expect("failure reported");

        let expected = reason("customer_unreachable", Some("rang twice"));
        assert_eq!(output.reason, expected);
        let delivery = world.delivery("delivery-1");
        assert_eq!(delivery.state(), State::Failed);
        assert_eq!(delivery.failure_reason(), Some(&expected));
        assert_eq!(world.courier_status("courier-1"), CourierStatus::Available);
        assert_eq!(
            world.store.outbox_messages(),
            vec![OutboxMessage::delivery(DomainEvent::DeliveryFailed {
                delivery_id: DeliveryId::new("delivery-1"),
                order_id: OrderId::new("order-of-delivery-1"),
                courier_id: CourierId::new("courier-1"),
                reason: expected,
                market_id: MarketId::new("berlin").expect("market id"),
            })]
        );
        assert_eq!(
            world.authorization.delivery_checks()[0].2,
            DeliveryResource::Courier(CourierId::new("courier-1"))
        );
    }

    /// AC-691: courier and dispatcher reports both revoke the courier's
    /// access; the revision of the DELETE is stored.
    #[test]
    fn failure_report_revokes_courier_relationship() {
        for (subject_id, reporter) in [
            ("courier-1", Reporter::Courier("courier-1".to_owned())),
            ("dispatcher-1", Reporter::Dispatcher),
        ] {
            let world = World::new();
            world.picked_up_delivery("delivery-1", "courier-1");

            handler(&world)
                .execute(
                    &subject(subject_id),
                    input(reporter, "customer_unreachable"),
                )
                .expect("failure reported");

            assert_eq!(
                world.relationships.writes(),
                vec![RelationshipWrite::UnassignCourier(
                    DeliveryId::new("delivery-1"),
                    CourierId::new("courier-1")
                )],
                "{subject_id}"
            );
            let delivery = world.delivery("delivery-1");
            assert_eq!(delivery.active_courier(), None);
            assert_eq!(
                delivery.authorization_revision(),
                Some(&RecordingRelationships::revision(1))
            );
        }
    }

    /// A failed DELETE rolls the failure report back (fail closed).
    #[test]
    fn failed_relationship_delete_rolls_failure_back() {
        let world = World {
            relationships: RecordingRelationships::failing("spicedb offline"),
            ..World::new()
        };
        world.picked_up_delivery("delivery-1", "courier-1");

        assert!(matches!(
            handler(&world).execute(
                &subject("courier-1"),
                input(
                    Reporter::Courier("courier-1".to_owned()),
                    "customer_unreachable"
                )
            ),
            Err(DeliveryApplicationError::DependencyUnavailable(_))
        ));
        assert_eq!(world.delivery_state("delivery-1"), State::PickedUp);
        assert_eq!(world.courier_status("courier-1"), CourierStatus::Busy);
        assert!(world.store.outbox_messages().is_empty());
    }

    /// AC-341
    #[test]
    fn dispatcher_reports_failure_of_assigned_delivery() {
        let world = World::new();
        world.assigned_delivery("delivery-1", "courier-1");

        let output = handler(&world)
            .execute(
                &subject("dispatcher-1"),
                input(Reporter::Dispatcher, "no_access"),
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
                input(Reporter::Dispatcher, "other")
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
                input(
                    Reporter::Courier("courier-2".to_owned()),
                    "courier_incident"
                )
            ),
            Err(DeliveryApplicationError::DeliveryPrecondition(
                DeliveryError::CourierNotAssigned
            ))
        );
        assert_eq!(world.delivery_state("delivery-1"), State::Assigned);
    }

    #[test]
    fn unknown_code_is_invalid_not_other() {
        let world = World::new();
        world.assigned_delivery("delivery-1", "courier-1");
        assert_eq!(
            handler(&world).execute(
                &subject("dispatcher-1"),
                input(Reporter::Dispatcher, "customer absent")
            ),
            Err(DeliveryApplicationError::InvalidRequest(
                InvalidDeliveryRequest::Validation(DeliveryValidationError::UnknownFailureReason(
                    "customer absent".to_owned()
                ))
            ))
        );
        assert_eq!(world.delivery_state("delivery-1"), State::Assigned);
        assert!(world.store.outbox_messages().is_empty());
    }

    #[test]
    fn denied_subject_cannot_report_failure() {
        let world = World::with_authorization(StaticAuthorization::deny_all());
        world.assigned_delivery("delivery-1", "courier-1");
        assert_eq!(
            handler(&world).execute(
                &subject("intruder"),
                input(Reporter::Dispatcher, "no_access")
            ),
            Err(DeliveryApplicationError::PermissionDenied)
        );
        assert_eq!(world.delivery_state("delivery-1"), State::Assigned);
    }
}

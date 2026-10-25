use super::fsm::{ensure_allowed, transition};
use super::{
    Address, DeliveryCancellationReason, DeliveryError, DeliveryFailureReason, DeliveryId,
    DeliveryValidationError, DomainEvent, OrderId, State, TransitionError, TransitionResult,
    Trigger,
};
use crate::pkg::persistence::AuthorizationRevision;
use crate::slices::courier::domains::courier::CourierId;

/// Actor reporting a Delivery Failure.
#[derive(Clone, Debug, Eq, PartialEq)]
pub enum FailureReporter {
    /// The courier must be the one assigned to the delivery.
    AssignedCourier(CourierId),
    /// An authorized dispatcher acting on the delivery.
    Dispatcher,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct Delivery {
    id: DeliveryId,
    order_id: OrderId,
    courier_id: Option<CourierId>,
    pickup_address: Address,
    dropoff_address: Address,
    state: State,
    failure_reason: Option<DeliveryFailureReason>,
    /// Technical field: revision of the last `delivery:<id>#courier` write.
    authorization_revision: Option<AuthorizationRevision>,
    events: Vec<DomainEvent>,
}

impl Delivery {
    #[must_use]
    pub fn new(
        id: DeliveryId,
        order_id: OrderId,
        pickup_address: Address,
        dropoff_address: Address,
    ) -> Self {
        Self {
            id,
            order_id,
            courier_id: None,
            pickup_address,
            dropoff_address,
            state: State::Waiting,
            failure_reason: None,
            authorization_revision: None,
            events: Vec::new(),
        }
    }

    pub fn restore(
        id: DeliveryId,
        order_id: OrderId,
        courier_id: Option<CourierId>,
        pickup_address: Address,
        dropoff_address: Address,
        state: State,
        failure_reason: Option<DeliveryFailureReason>,
    ) -> Result<Self, DeliveryValidationError> {
        if state == State::Waiting && courier_id.is_some() {
            return Err(DeliveryValidationError::WaitingStateMustNotHaveCourier);
        }
        if state == State::Cancelled && courier_id.is_some() {
            return Err(DeliveryValidationError::CancelledStateMustNotHaveCourier);
        }
        if !matches!(state, State::Waiting | State::Cancelled) && courier_id.is_none() {
            return Err(DeliveryValidationError::AssignedStateRequiresCourier);
        }
        if state == State::Failed && failure_reason.is_none() {
            return Err(DeliveryValidationError::FailedStateRequiresReason);
        }
        if state != State::Failed && failure_reason.is_some() {
            return Err(DeliveryValidationError::FailureReasonRequiresFailedState);
        }
        Ok(Self {
            id,
            order_id,
            courier_id,
            pickup_address,
            dropoff_address,
            state,
            failure_reason,
            authorization_revision: None,
            events: Vec::new(),
        })
    }

    /// Snapshot field `authorization_revision`, absent in old snapshots.
    #[must_use]
    pub fn with_authorization_revision(mut self, revision: Option<AuthorizationRevision>) -> Self {
        self.authorization_revision = revision;
        self
    }

    /// Stores the revision of a relationship write (`TOUCH` or `DELETE`) made
    /// for this delivery.
    pub fn record_authorization_revision(&mut self, revision: AuthorizationRevision) {
        self.authorization_revision = Some(revision);
    }

    #[must_use]
    pub fn authorization_revision(&self) -> Option<&AuthorizationRevision> {
        self.authorization_revision.as_ref()
    }

    /// Checks without changing state that the current state permits a Courier
    /// Assignment. The courier itself is checked by [`Delivery::assign`].
    pub fn ensure_assignable(&self) -> Result<(), TransitionError> {
        ensure_allowed(self.state, Trigger::Assign)
    }

    pub fn assign(&mut self, courier_id: CourierId) -> Result<TransitionResult, TransitionError> {
        let result = transition(self.state, Trigger::Assign, Some(&courier_id))?;
        self.events.push(DomainEvent::CourierAssignedToDelivery {
            delivery_id: self.id.clone(),
            order_id: self.order_id.clone(),
            courier_id: courier_id.clone(),
        });
        self.courier_id = Some(courier_id);
        self.state = result.to;
        Ok(result)
    }

    /// Ends the current Courier Assignment and returns the delivery to the
    /// search for a replacement.
    pub fn unassign(&mut self) -> Result<TransitionResult, TransitionError> {
        let result = transition(self.state, Trigger::Unassign, self.courier_id.as_ref())?;
        if let Some(courier_id) = self.courier_id.take() {
            self.events
                .push(DomainEvent::CourierUnassignedFromDelivery {
                    delivery_id: self.id.clone(),
                    order_id: self.order_id.clone(),
                    courier_id,
                });
        }
        self.state = result.to;
        Ok(result)
    }

    pub fn confirm_pickup(
        &mut self,
        courier_id: &CourierId,
    ) -> Result<TransitionResult, DeliveryError> {
        let result = self.transition_by(Trigger::Pickup, courier_id)?;
        self.state = result.to;
        self.events.push(DomainEvent::DeliveryPickedUp {
            delivery_id: self.id.clone(),
            order_id: self.order_id.clone(),
            courier_id: courier_id.clone(),
        });
        Ok(result)
    }

    pub fn complete(&mut self, courier_id: &CourierId) -> Result<TransitionResult, DeliveryError> {
        let result = self.transition_by(Trigger::Complete, courier_id)?;
        self.state = result.to;
        self.events.push(DomainEvent::DeliveryCompleted {
            delivery_id: self.id.clone(),
            order_id: self.order_id.clone(),
            courier_id: courier_id.clone(),
        });
        Ok(result)
    }

    pub fn fail(
        &mut self,
        reason: DeliveryFailureReason,
        reporter: &FailureReporter,
    ) -> Result<TransitionResult, DeliveryError> {
        let result = match reporter {
            FailureReporter::AssignedCourier(courier_id) => {
                self.transition_by(Trigger::Fail, courier_id)?
            }
            FailureReporter::Dispatcher => {
                transition(self.state, Trigger::Fail, self.courier_id.as_ref())?
            }
        };
        let Some(courier_id) = self.courier_id.clone() else {
            return Err(DeliveryError::CourierNotAssigned);
        };
        self.state = result.to;
        self.failure_reason = Some(reason.clone());
        self.events.push(DomainEvent::DeliveryFailed {
            delivery_id: self.id.clone(),
            order_id: self.order_id.clone(),
            courier_id,
            reason,
        });
        Ok(result)
    }

    /// Cancels a Delivery that is not yet picked up. The current Courier
    /// Assignment, if any, ends; the caller releases the returned courier.
    /// A repeated cancellation yields [`TransitionError::AlreadyInState`].
    pub fn cancel(
        &mut self,
        reason: Option<DeliveryCancellationReason>,
    ) -> Result<Option<CourierId>, TransitionError> {
        let result = transition(self.state, Trigger::Cancel, self.courier_id.as_ref())?;
        let released_courier_id = self.courier_id.take();
        self.state = result.to;
        self.events.push(DomainEvent::DeliveryCancelled {
            delivery_id: self.id.clone(),
            order_id: self.order_id.clone(),
            released_courier_id: released_courier_id.clone(),
            reason,
        });
        Ok(released_courier_id)
    }

    /// Resolves a transition that only the assigned courier may trigger: the
    /// state check comes first, then the actor check.
    fn transition_by(
        &self,
        trigger: Trigger,
        courier_id: &CourierId,
    ) -> Result<TransitionResult, DeliveryError> {
        let result = transition(self.state, trigger, self.courier_id.as_ref())?;
        if self.courier_id.as_ref() != Some(courier_id) {
            return Err(DeliveryError::CourierNotAssigned);
        }
        Ok(result)
    }

    pub fn pull_events(&mut self) -> Vec<DomainEvent> {
        std::mem::take(&mut self.events)
    }

    #[must_use]
    pub const fn state(&self) -> State {
        self.state
    }

    #[must_use]
    pub fn courier_id(&self) -> Option<&CourierId> {
        self.courier_id.as_ref()
    }

    #[must_use]
    pub fn id(&self) -> &DeliveryId {
        &self.id
    }

    #[must_use]
    pub fn order_id(&self) -> &OrderId {
        &self.order_id
    }

    #[must_use]
    pub fn pickup_address(&self) -> &Address {
        &self.pickup_address
    }

    #[must_use]
    pub fn dropoff_address(&self) -> &Address {
        &self.dropoff_address
    }

    #[must_use]
    pub fn failure_reason(&self) -> Option<&DeliveryFailureReason> {
        self.failure_reason.as_ref()
    }

    /// Whether the delivery holds an active Courier Assignment.
    #[must_use]
    pub const fn is_active_assignment(&self) -> bool {
        matches!(self.state, State::Assigned | State::PickedUp)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn delivery() -> Delivery {
        Delivery::new(
            DeliveryId::new("delivery-1"),
            OrderId::new("order-1"),
            Address::new(" Restaurant  street 1 ").expect("pickup address"),
            Address::new("Customer street 2").expect("dropoff address"),
        )
    }

    fn assigned(courier: &str) -> Delivery {
        let mut delivery = delivery();
        delivery.assign(CourierId::new(courier)).expect("assign");
        delivery.pull_events();
        delivery
    }

    fn reason() -> DeliveryFailureReason {
        DeliveryFailureReason::new("customer unreachable").expect("reason")
    }

    #[test]
    fn delivery_lifecycle_is_strict_and_records_facts() {
        let mut delivery = delivery();
        let courier_id = CourierId::new("courier-1");

        assert_eq!(
            delivery.assign(courier_id.clone()).expect("assign").to,
            State::Assigned
        );
        assert_eq!(
            delivery
                .assign(courier_id.clone())
                .expect_err("duplicate assignment"),
            TransitionError::AlreadyInState {
                state: State::Assigned
            }
        );
        assert_eq!(
            delivery.confirm_pickup(&courier_id).expect("pickup").to,
            State::PickedUp
        );
        assert_eq!(
            delivery.complete(&courier_id).expect("completion").to,
            State::Delivered
        );
        let order_id = OrderId::new("order-1");
        let delivery_id = DeliveryId::new("delivery-1");
        assert_eq!(
            delivery.pull_events(),
            vec![
                DomainEvent::CourierAssignedToDelivery {
                    delivery_id: delivery_id.clone(),
                    order_id: order_id.clone(),
                    courier_id: courier_id.clone(),
                },
                DomainEvent::DeliveryPickedUp {
                    delivery_id: delivery_id.clone(),
                    order_id: order_id.clone(),
                    courier_id: courier_id.clone(),
                },
                DomainEvent::DeliveryCompleted {
                    delivery_id,
                    order_id,
                    courier_id,
                },
            ]
        );
        assert!(delivery.pull_events().is_empty());
    }

    #[test]
    fn invalid_transition_does_not_change_state() {
        let mut delivery = delivery();
        assert_eq!(
            delivery
                .confirm_pickup(&CourierId::new("courier-1"))
                .expect_err("pickup before assignment"),
            DeliveryError::Transition(TransitionError::InvalidTransition {
                from: State::Waiting,
                trigger: Trigger::Pickup
            })
        );
        assert_eq!(delivery.state(), State::Waiting);
        assert!(delivery.pull_events().is_empty());
    }

    #[test]
    fn only_assigned_courier_confirms_pickup_and_completion() {
        let mut delivery = assigned("courier-1");
        let other = CourierId::new("courier-2");

        assert_eq!(
            delivery.confirm_pickup(&other),
            Err(DeliveryError::CourierNotAssigned)
        );
        assert_eq!(delivery.state(), State::Assigned);

        delivery
            .confirm_pickup(&CourierId::new("courier-1"))
            .expect("pickup");
        assert_eq!(
            delivery.complete(&other),
            Err(DeliveryError::CourierNotAssigned)
        );
        assert_eq!(delivery.state(), State::PickedUp);
    }

    #[test]
    fn unassign_returns_delivery_to_waiting() {
        let mut delivery = assigned("courier-1");

        assert_eq!(delivery.unassign().expect("unassign").to, State::Waiting);
        assert_eq!(delivery.courier_id(), None);
        assert_eq!(
            delivery.pull_events(),
            vec![DomainEvent::CourierUnassignedFromDelivery {
                delivery_id: DeliveryId::new("delivery-1"),
                order_id: OrderId::new("order-1"),
                courier_id: CourierId::new("courier-1"),
            }]
        );
        assert_eq!(
            delivery.unassign(),
            Err(TransitionError::InvalidTransition {
                from: State::Waiting,
                trigger: Trigger::Unassign
            })
        );
    }

    #[test]
    fn failure_is_recorded_with_reason_by_courier_or_dispatcher() {
        let mut by_courier = assigned("courier-1");
        by_courier
            .fail(
                reason(),
                &FailureReporter::AssignedCourier(CourierId::new("courier-1")),
            )
            .expect("courier reports failure");
        assert_eq!(by_courier.state(), State::Failed);
        assert_eq!(by_courier.failure_reason(), Some(&reason()));
        assert_eq!(
            by_courier.pull_events(),
            vec![DomainEvent::DeliveryFailed {
                delivery_id: DeliveryId::new("delivery-1"),
                order_id: OrderId::new("order-1"),
                courier_id: CourierId::new("courier-1"),
                reason: reason(),
            }]
        );

        let mut by_dispatcher = assigned("courier-1");
        by_dispatcher
            .fail(reason(), &FailureReporter::Dispatcher)
            .expect("dispatcher reports failure");
        assert_eq!(by_dispatcher.state(), State::Failed);

        let mut by_other = assigned("courier-1");
        assert_eq!(
            by_other.fail(
                reason(),
                &FailureReporter::AssignedCourier(CourierId::new("courier-2"))
            ),
            Err(DeliveryError::CourierNotAssigned)
        );
        assert_eq!(by_other.state(), State::Assigned);
    }

    #[test]
    fn waiting_delivery_cannot_fail() {
        let mut delivery = delivery();
        assert_eq!(
            delivery.fail(reason(), &FailureReporter::Dispatcher),
            Err(DeliveryError::Transition(
                TransitionError::InvalidTransition {
                    from: State::Waiting,
                    trigger: Trigger::Fail
                }
            ))
        );
    }

    #[test]
    fn cancellation_before_pickup_releases_the_courier() {
        let mut cancelled = assigned("courier-1");
        let reason = DeliveryCancellationReason::new("order cancelled");

        assert_eq!(
            cancelled.cancel(reason.clone()),
            Ok(Some(CourierId::new("courier-1")))
        );
        assert_eq!(cancelled.state(), State::Cancelled);
        assert_eq!(cancelled.courier_id(), None);
        assert_eq!(
            cancelled.pull_events(),
            vec![DomainEvent::DeliveryCancelled {
                delivery_id: DeliveryId::new("delivery-1"),
                order_id: OrderId::new("order-1"),
                released_courier_id: Some(CourierId::new("courier-1")),
                reason,
            }]
        );
        assert_eq!(
            cancelled.cancel(None),
            Err(TransitionError::AlreadyInState {
                state: State::Cancelled
            })
        );
        assert!(cancelled.pull_events().is_empty());

        let mut waiting = delivery();
        assert_eq!(waiting.cancel(None), Ok(None));
        assert_eq!(waiting.state(), State::Cancelled);
    }

    #[test]
    fn picked_up_delivery_cannot_be_cancelled() {
        let mut delivery = assigned("courier-1");
        delivery
            .confirm_pickup(&CourierId::new("courier-1"))
            .expect("pickup");
        delivery.pull_events();

        assert_eq!(
            delivery.cancel(None),
            Err(TransitionError::InvalidTransition {
                from: State::PickedUp,
                trigger: Trigger::Cancel
            })
        );
        assert_eq!(delivery.state(), State::PickedUp);
        assert_eq!(delivery.courier_id(), Some(&CourierId::new("courier-1")));
        assert!(delivery.pull_events().is_empty());
    }

    #[test]
    fn ensure_assignable_does_not_change_state() {
        let delivery = delivery();
        assert_eq!(delivery.ensure_assignable(), Ok(()));
        assert_eq!(delivery.state(), State::Waiting);
        assert_eq!(
            assigned("courier-2").ensure_assignable(),
            Err(TransitionError::AlreadyInState {
                state: State::Assigned
            })
        );
    }

    #[test]
    fn restore_validates_courier_and_failure_reason() {
        let restore = |courier: Option<&str>, state, reason| {
            Delivery::restore(
                DeliveryId::new("delivery-1"),
                OrderId::new("order-1"),
                courier.map(CourierId::new),
                Address::new("a").expect("address"),
                Address::new("b").expect("address"),
                state,
                reason,
            )
        };
        assert_eq!(
            restore(Some("c"), State::Waiting, None).expect_err("waiting with courier"),
            DeliveryValidationError::WaitingStateMustNotHaveCourier
        );
        assert_eq!(
            restore(None, State::Assigned, None).expect_err("assigned without courier"),
            DeliveryValidationError::AssignedStateRequiresCourier
        );
        assert_eq!(
            restore(Some("c"), State::Failed, None).expect_err("failed without reason"),
            DeliveryValidationError::FailedStateRequiresReason
        );
        assert_eq!(
            restore(Some("c"), State::Delivered, Some(reason())).expect_err("reason not failed"),
            DeliveryValidationError::FailureReasonRequiresFailedState
        );
        assert_eq!(
            restore(Some("c"), State::Cancelled, None).expect_err("cancelled with courier"),
            DeliveryValidationError::CancelledStateMustNotHaveCourier
        );
        assert!(restore(None, State::Cancelled, None).is_ok());
        assert!(restore(Some("c"), State::Failed, Some(reason())).is_ok());
    }
}

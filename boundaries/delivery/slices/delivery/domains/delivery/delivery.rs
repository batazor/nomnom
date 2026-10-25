use super::fsm::{ensure_allowed, transition};
use super::{
    Address, DeliveryCancellationReason, DeliveryError, DeliveryFailureReason, DeliveryId,
    DeliveryValidationError, DomainEvent, ExpectedTime, OrderId, State, TransitionError,
    TransitionResult, Trigger,
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
    state: State,
    failure_reason: Option<DeliveryFailureReason>,
    /// Pickup time named by the courier of the current Courier Assignment;
    /// `None` — not named (FR-610).
    expected_pickup_at: Option<ExpectedTime>,
    /// Handover time named by the courier on Pickup; `None` — not named.
    expected_delivered_at: Option<ExpectedTime>,
    /// Creation time; `None` for a Delivery stored before it was recorded.
    created_at_epoch_seconds: Option<i64>,
    /// Technical field: revision of the last `delivery:<id>#courier` write.
    authorization_revision: Option<AuthorizationRevision>,
    /// Technical field: optimistic concurrency version of the stored snapshot.
    version: u64,
    events: Vec<DomainEvent>,
}

impl Delivery {
    #[must_use]
    pub fn new(id: DeliveryId, order_id: OrderId, pickup_address: Address) -> Self {
        Self {
            id,
            order_id,
            courier_id: None,
            pickup_address,
            state: State::Waiting,
            failure_reason: None,
            expected_pickup_at: None,
            expected_delivered_at: None,
            created_at_epoch_seconds: None,
            authorization_revision: None,
            version: 0,
            events: Vec::new(),
        }
    }

    /// Creates a waiting Delivery at `created_at_epoch_seconds` and records
    /// [`DomainEvent::DeliveryCreated`].
    #[must_use]
    pub fn create(
        id: DeliveryId,
        order_id: OrderId,
        pickup_address: Address,
        created_at_epoch_seconds: i64,
    ) -> Self {
        let mut delivery =
            Self::new(id, order_id, pickup_address).with_created_at(Some(created_at_epoch_seconds));
        delivery.events.push(DomainEvent::DeliveryCreated {
            delivery_id: delivery.id.clone(),
            order_id: delivery.order_id.clone(),
            created_at_epoch_seconds,
        });
        delivery
    }

    pub fn restore(
        id: DeliveryId,
        order_id: OrderId,
        courier_id: Option<CourierId>,
        pickup_address: Address,
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
            state,
            failure_reason,
            expected_pickup_at: None,
            expected_delivered_at: None,
            created_at_epoch_seconds: None,
            authorization_revision: None,
            version: 0,
            events: Vec::new(),
        })
    }

    /// Snapshot field `version` (optimistic concurrency): the stored version
    /// this aggregate was loaded at; `0` for a new aggregate.
    #[must_use]
    pub fn with_version(mut self, version: u64) -> Self {
        self.version = version;
        self
    }

    #[must_use]
    pub const fn version(&self) -> u64 {
        self.version
    }

    /// Snapshot field `created_at`, absent in old snapshots.
    #[must_use]
    pub fn with_created_at(mut self, created_at_epoch_seconds: Option<i64>) -> Self {
        self.created_at_epoch_seconds = created_at_epoch_seconds;
        self
    }

    #[must_use]
    pub const fn created_at_epoch_seconds(&self) -> Option<i64> {
        self.created_at_epoch_seconds
    }

    /// Snapshot fields `expected_pickup_at` and `expected_delivered_at`.
    #[must_use]
    pub fn with_expected_times(
        mut self,
        expected_pickup_at: Option<ExpectedTime>,
        expected_delivered_at: Option<ExpectedTime>,
    ) -> Self {
        self.expected_pickup_at = expected_pickup_at;
        self.expected_delivered_at = expected_delivered_at;
        self
    }

    #[must_use]
    pub const fn expected_pickup_at(&self) -> Option<ExpectedTime> {
        self.expected_pickup_at
    }

    #[must_use]
    pub const fn expected_delivered_at(&self) -> Option<ExpectedTime> {
        self.expected_delivered_at
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

    /// Starts a Courier Assignment; `expected_pickup_at` is the Pickup time
    /// the courier named, it replaces the one of a previous assignment.
    pub fn assign(
        &mut self,
        courier_id: CourierId,
        expected_pickup_at: Option<ExpectedTime>,
    ) -> Result<TransitionResult, TransitionError> {
        let result = transition(self.state, Trigger::Assign, Some(&courier_id))?;
        self.events.push(DomainEvent::CourierAssignedToDelivery {
            delivery_id: self.id.clone(),
            order_id: self.order_id.clone(),
            courier_id: courier_id.clone(),
            created_at_epoch_seconds: self.created_at_epoch_seconds,
            expected_pickup_at_epoch_seconds: expected_pickup_at.map(ExpectedTime::epoch_seconds),
        });
        self.courier_id = Some(courier_id);
        self.expected_pickup_at = expected_pickup_at;
        self.state = result.to;
        Ok(result)
    }

    /// Ends the current Courier Assignment and returns the delivery to the
    /// search for a replacement; the Pickup time of the ended assignment is
    /// dropped.
    pub fn unassign(&mut self) -> Result<TransitionResult, TransitionError> {
        let result = transition(self.state, Trigger::Unassign, self.courier_id.as_ref())?;
        self.expected_pickup_at = None;
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

    /// Records the Pickup; `expected_delivered_at` is the handover time the
    /// courier named.
    pub fn confirm_pickup(
        &mut self,
        courier_id: &CourierId,
        expected_delivered_at: Option<ExpectedTime>,
    ) -> Result<TransitionResult, DeliveryError> {
        let result = self.transition_by(Trigger::Pickup, courier_id)?;
        self.state = result.to;
        self.expected_delivered_at = expected_delivered_at;
        self.events.push(DomainEvent::DeliveryPickedUp {
            delivery_id: self.id.clone(),
            order_id: self.order_id.clone(),
            courier_id: courier_id.clone(),
            expected_delivered_at_epoch_seconds: expected_delivered_at
                .map(ExpectedTime::epoch_seconds),
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
            created_at_epoch_seconds: self.created_at_epoch_seconds,
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
        self.expected_pickup_at = None;
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

    /// Courier of the active Courier Assignment: `courier_id` while the
    /// delivery is assigned or picked up. A delivered or failed delivery
    /// keeps `courier_id` as the fact of who carried it, but the assignment
    /// and the access it grants are over (FR-444).
    #[must_use]
    pub fn active_courier(&self) -> Option<&CourierId> {
        self.courier_id
            .as_ref()
            .filter(|_| self.state.has_active_assignment())
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
#[path = "delivery/tests.rs"]
mod tests;

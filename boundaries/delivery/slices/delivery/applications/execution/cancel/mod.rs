//! Cancel Delivery: the fulfillment process of `orders` cancels the Delivery
//! of an order before Pickup. A waiting or assigned delivery becomes
//! cancelled, the assigned courier is released and the fact is written to the
//! Outbox in one Unit of Work. A repeated cancellation succeeds without a new
//! effect; after Pickup or a final state the cancellation is a business
//! rejection, not an error. When a courier is released,
//! `delivery:<id>#courier@user:<courier_id>` is deleted from the authorization
//! graph before commit; a failed delete rolls the cancellation back. After
//! commit the cancellation ends the Assignment Workflow of the delivery, and
//! the released courier wakes the search of its Market (FR-706).

use crate::pkg::authz::Subject;
use crate::pkg::unit_of_work::UnitOfWork;
use crate::slices::courier::applications::assignment::CourierAssignment;
use crate::slices::courier::applications::candidate_wake::CandidateWake;
use crate::slices::courier::domains::courier::CourierId;
use crate::slices::delivery::domains::delivery::{
    CompoundState, DeliveryCancellationReason, DeliveryId, DeliveryRepository, OrderId, State,
    TransitionError,
};

use super::super::assignment::release_assignment;
use super::super::assignment_process::{AssignmentProcess, WakeReason};
use super::super::authorization::{DeliveryAuthorization, DeliveryPermission, DeliveryResource};
use super::super::error::DeliveryApplicationError;
use super::super::outbox::{DeliveryOutbox, publish};
use super::super::relationships::DeliveryRelationshipWriter;
use super::super::{load_delivery_by_order, order_id};

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct Input {
    pub order_id: String,
    /// Optional free-form reason; blank means no reason.
    pub reason: String,
}

/// Why the delivery state forbids the cancellation.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum CancellationRejection {
    /// The courier already picked the order up.
    AlreadyPickedUp,
    /// The delivery is already completed or failed.
    AlreadyClosed,
}

impl CancellationRejection {
    /// Stable API code of the rejection.
    #[must_use]
    pub const fn as_str(self) -> &'static str {
        match self {
            Self::AlreadyPickedUp => "already_picked_up",
            Self::AlreadyClosed => "already_closed",
        }
    }
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub enum Outcome {
    Cancelled {
        /// Courier released by this call; `None` for a waiting delivery or a
        /// repeated cancellation.
        released_courier_id: Option<CourierId>,
        /// `true` when an earlier request already cancelled the delivery.
        already_cancelled: bool,
    },
    Rejected(CancellationRejection),
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct Output {
    pub delivery_id: DeliveryId,
    pub outcome: Outcome,
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
        let order_id = order_id(&input.order_id)?;
        let reason = DeliveryCancellationReason::new(&input.reason);
        self.authorization.check(
            subject,
            DeliveryPermission::CancelDelivery,
            &DeliveryResource::Order(order_id.clone()),
        )?;

        let output = self
            .unit_of_work
            .run(|| self.cancel(&order_id, reason.clone()))?;
        if let Outcome::Cancelled {
            released_courier_id,
            ..
        } = &output.outcome
        {
            // Also on a repeat: the wake of the first call may have been lost.
            self.assignment
                .wake(&output.delivery_id, WakeReason::DeliveryClosed)?;
            if let Some(courier_id) = released_courier_id {
                self.candidates.courier_available(courier_id)?;
            }
        }
        Ok(output)
    }

    fn cancel(
        &self,
        order_id: &OrderId,
        reason: Option<DeliveryCancellationReason>,
    ) -> Result<Output, DeliveryApplicationError> {
        let mut delivery = load_delivery_by_order(self.deliveries, order_id)?;
        let delivery_id = delivery.id().clone();
        let outcome = match delivery.cancel(reason) {
            Ok(released_courier_id) => {
                if let Some(courier_id) = &released_courier_id {
                    release_assignment(
                        &mut delivery,
                        courier_id,
                        self.couriers,
                        self.relationships,
                    )?;
                }
                self.deliveries.save(&delivery)?;
                publish(self.outbox, delivery.pull_events())?;
                Outcome::Cancelled {
                    released_courier_id,
                    already_cancelled: false,
                }
            }
            Err(TransitionError::AlreadyInState {
                state: State::Cancelled,
            }) => Outcome::Cancelled {
                released_courier_id: None,
                already_cancelled: true,
            },
            Err(TransitionError::InvalidTransition {
                from: State::PickedUp,
                ..
            }) => Outcome::Rejected(CancellationRejection::AlreadyPickedUp),
            Err(TransitionError::InvalidTransition { from, .. })
                if from.is_in(CompoundState::Closed) =>
            {
                Outcome::Rejected(CancellationRejection::AlreadyClosed)
            }
            Err(error) => return Err(error.into()),
        };
        Ok(Output {
            delivery_id,
            outcome,
        })
    }
}

#[cfg(test)]
mod tests;

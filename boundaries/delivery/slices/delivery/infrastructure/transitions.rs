//! FSM transitions of the `delivery` aggregates (fsm section of the inventory
//! ops/grafana/inventory; registry ops/semconv/model/fsm.yaml): one
//! `nomnom.fsm.transitions` point per transition of `Delivery` or
//! `DeliveryOffer`. The FSM stays pure (ADR 21): transitions are observed
//! from the outside.
//!
//! - `applied`: a stored state change, read as the transition of the table
//!   (`applied_transition`, `applied_offer_transition`) by the repository
//!   decorator [`ObservedOutbox`](super::observability::ObservedOutbox) and
//!   recorded only after the outermost Unit of Work commits. A rollback or a
//!   lost optimistic concurrency race records nothing.
//! - `rejected`: a use case that failed with `InvalidTransition`
//!   ([`ObservedDeliveryUseCases`](super::observed_use_cases::ObservedDeliveryUseCases)).
//!   The state does not change, so `nomnom.fsm.to_state` is the
//!   `nomnom.fsm.from_state` and `nomnom.fsm.terminal` is `false`.
//!   `AlreadyInState` is a repeat the handler policy decides on, and a failed
//!   guard carries no state: neither is recorded.
//!
//! Attributes: `nomnom.fsm.aggregate_type` (`delivery` | `delivery_offer`),
//! `nomnom.fsm.from_state` / `nomnom.fsm.to_state` (`waiting` | `assigned` |
//! `picked_up` | `delivered` | `failed` | `cancelled`; `pending` | `accepted` |
//! `rejected` | `withdrawn` | `expired`), `nomnom.fsm.trigger` (`assign` |
//! `unassign` | `pickup` | `complete` | `fail` | `cancel`; `accept` | `reject`
//! | `withdraw` | `expire`), `nomnom.fsm.result` (`applied` | `rejected`) and
//! `nomnom.fsm.terminal` (boolean).

use opentelemetry::KeyValue;
use opentelemetry::metrics::{Counter, Meter};

use crate::slices::delivery::applications::error::DeliveryApplicationError;
use crate::slices::delivery::domains::delivery::{
    DeliveryError, DeliveryOfferError, OfferState, OfferTransitionResult, OfferTrigger, State,
    TransitionError, TransitionResult, Trigger,
};

/// Instrumentation scope of the FSM metrics.
pub const METER_NAME: &str = "nomnom.delivery.fsm";

const DELIVERY: &str = "delivery";
const DELIVERY_OFFER: &str = "delivery_offer";
const APPLIED: &str = "applied";
const REJECTED: &str = "rejected";

/// An applied transition of one aggregate.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum Transition {
    Delivery(TransitionResult),
    Offer(OfferTransitionResult),
}

/// Attributes of one transition point.
struct Point {
    aggregate_type: &'static str,
    from: &'static str,
    trigger: &'static str,
    to: &'static str,
    result: &'static str,
    terminal: bool,
}

/// Counter `nomnom.fsm.transitions`.
#[derive(Clone)]
pub struct TransitionMetrics {
    transitions: Counter<u64>,
}

impl TransitionMetrics {
    /// Instruments of the global MeterProvider (no-op until the pipeline
    /// starts).
    #[must_use]
    pub fn global() -> Self {
        Self::new(&opentelemetry::global::meter(METER_NAME))
    }

    #[must_use]
    pub fn new(meter: &Meter) -> Self {
        Self {
            transitions: meter
                .u64_counter("nomnom.fsm.transitions")
                .with_unit("{transition}")
                .with_description("Applied FSM transitions.")
                .build(),
        }
    }

    /// One committed transition.
    pub fn applied(&self, transition: Transition) {
        self.record(&match transition {
            Transition::Delivery(result) => Point {
                aggregate_type: DELIVERY,
                from: state(result.from),
                trigger: trigger(result.trigger),
                to: state(result.to),
                result: APPLIED,
                terminal: result.to.is_terminal(),
            },
            Transition::Offer(result) => Point {
                aggregate_type: DELIVERY_OFFER,
                from: offer_state(result.from),
                trigger: offer_trigger(result.trigger),
                to: offer_state(result.to),
                result: APPLIED,
                terminal: result.to.is_terminal(),
            },
        });
    }

    /// The transition a failed use case was refused, if it was one.
    pub fn rejected(&self, error: &DeliveryApplicationError) {
        let (aggregate_type, from, trigger) = match error {
            DeliveryApplicationError::DeliveryPrecondition(DeliveryError::Transition(
                TransitionError::InvalidTransition { from, trigger: by },
            )) => (DELIVERY, state(*from), trigger(*by)),
            DeliveryApplicationError::OfferPrecondition(
                DeliveryOfferError::InvalidTransition { from, trigger: by },
            ) => (DELIVERY_OFFER, offer_state(*from), offer_trigger(*by)),
            _ => return,
        };
        self.record(&Point {
            aggregate_type,
            from,
            trigger,
            to: from,
            result: REJECTED,
            terminal: false,
        });
    }

    fn record(&self, point: &Point) {
        self.transitions.add(
            1,
            &[
                KeyValue::new("nomnom.fsm.aggregate_type", point.aggregate_type),
                KeyValue::new("nomnom.fsm.from_state", point.from),
                KeyValue::new("nomnom.fsm.trigger", point.trigger),
                KeyValue::new("nomnom.fsm.to_state", point.to),
                KeyValue::new("nomnom.fsm.result", point.result),
                KeyValue::new("nomnom.fsm.terminal", point.terminal),
            ],
        );
    }
}

fn state(state: State) -> &'static str {
    match state {
        State::Waiting => "waiting",
        State::Assigned => "assigned",
        State::PickedUp => "picked_up",
        State::Delivered => "delivered",
        State::Failed => "failed",
        State::Cancelled => "cancelled",
    }
}

fn trigger(trigger: Trigger) -> &'static str {
    match trigger {
        Trigger::Assign => "assign",
        Trigger::Unassign => "unassign",
        Trigger::Pickup => "pickup",
        Trigger::Complete => "complete",
        Trigger::Fail => "fail",
        Trigger::Cancel => "cancel",
    }
}

fn offer_state(state: OfferState) -> &'static str {
    match state {
        OfferState::Pending => "pending",
        OfferState::Accepted => "accepted",
        OfferState::Rejected => "rejected",
        OfferState::Withdrawn => "withdrawn",
        OfferState::Expired => "expired",
    }
}

fn offer_trigger(trigger: OfferTrigger) -> &'static str {
    match trigger {
        OfferTrigger::Accept => "accept",
        OfferTrigger::Reject => "reject",
        OfferTrigger::Withdraw => "withdraw",
        OfferTrigger::Expire => "expire",
    }
}

#[cfg(test)]
#[path = "transitions/tests.rs"]
mod tests;

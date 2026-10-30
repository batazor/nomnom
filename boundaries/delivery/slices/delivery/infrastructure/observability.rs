//! Business metrics of `delivery` (domain section of the inventory
//! ops/grafana/inventory; names, units and attributes follow the registry
//! ops/semconv/model/domain.yaml). Same design as `orders` (`ObservedStore`)
//! and `payments`: a counter changes only at an idempotently confirmed
//! transition. [`ObservedOutbox`] decorates the Unit of Work and Outbox ports,
//! collects the Domain Events handed to the Outbox inside the Unit of Work and
//! records them only after the outermost Unit of Work commits. A rollback
//! records nothing; an idempotent repeat writes no new events, so it records
//! nothing either; a nested Unit of Work is recorded once, at the outer
//! commit. The domain does not know about metrics.
//!
//! Event → metric:
//! - `DeliveryCreated` → `nomnom.delivery.created`;
//! - `CourierAssignedToDelivery` → `nomnom.delivery.assignments` and
//!   `nomnom.delivery.assignment.latency` (creation → assignment commit);
//! - `CourierUnassignedFromDelivery` → `nomnom.delivery.reassignments`; the
//!   domain has no unassignment reason, so `nomnom.reason` is `other`;
//! - `DeliveryOfferAccepted` / `DeliveryOfferRejected` /
//!   `DeliveryOfferExpired` → `nomnom.delivery.offers` with `nomnom.outcome`
//!   accepted / rejected / expired;
//! - `DeliveryPickedUp` → `nomnom.delivery.pickups`;
//! - `DeliveryCompleted` → `nomnom.delivery.completed` and
//!   `nomnom.delivery.duration` (creation → completion commit);
//! - `DeliveryFailed` → `nomnom.delivery.failed` with `nomnom.reason` = the
//!   code of the Delivery Failure Reason catalog when it is in the closed set
//!   given by the composition root, otherwise `other`; the free-text detail is
//!   never an attribute;
//! - `DeliveryOffered`, `DeliveryCancelled` → no metric in the registry.
//!
//! A committed `DeliveryFailed` is also logged and set on the active span by
//! [`FailureLog`] when the composition root passes one.
//!
//! The same decorator observes the repositories of `Delivery` and
//! `DeliveryOffer` inside a Unit of Work: it remembers the state an aggregate
//! was loaded (or added) with, and a save that changes it is the applied FSM
//! transition of [`TransitionMetrics`], recorded with the events after the
//! outermost commit.

use std::collections::HashMap;
use std::sync::{Arc, Mutex, MutexGuard, PoisonError};
use std::thread::{self, ThreadId};
use std::time::{SystemTime, UNIX_EPOCH};

use opentelemetry::KeyValue;
use opentelemetry::metrics::{Counter, Histogram, Meter};

use crate::pkg::persistence::RepositoryError;
use crate::pkg::unit_of_work::{TransactionScope, UnitOfWorkError};
use crate::slices::delivery::applications::outbox::{DeliveryOutbox, OutboxMessage};
use crate::slices::delivery::domains::delivery::{
    Delivery, DeliveryId, DeliveryOffer, DeliveryOfferId, DeliveryOfferRepository,
    DeliveryRepository, DomainEvent, OfferState, OrderId, State, applied_offer_transition,
    applied_transition,
};

use super::failure_log::{FailureLog, ReportedFailure};
use super::transitions::{Transition, TransitionMetrics};

/// Instrumentation scope of the business metrics.
pub const METER_NAME: &str = "nomnom.delivery.domain";

const REASON: &str = "nomnom.reason";
const OUTCOME: &str = "nomnom.outcome";

/// `nomnom.reason` of a value outside the closed set.
pub const OTHER_REASON: &str = "other";

/// Creation → courier assignment: seconds to an hour.
const ASSIGNMENT_LATENCY_BUCKETS: [f64; 12] = [
    5.0, 15.0, 30.0, 60.0, 120.0, 300.0, 600.0, 900.0, 1200.0, 1800.0, 2700.0, 3600.0,
];

/// Creation → completion: minutes to hours.
const DURATION_BUCKETS: [f64; 12] = [
    300.0, 600.0, 900.0, 1200.0, 1800.0, 2700.0, 3600.0, 5400.0, 7200.0, 10800.0, 14400.0, 21600.0,
];

/// Current time in epoch seconds at the moment of recording.
pub type EpochClock = Arc<dyn Fn() -> i64 + Send + Sync>;

fn system_now() -> i64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map_or(0, |elapsed| {
            i64::try_from(elapsed.as_secs()).unwrap_or(i64::MAX)
        })
}

/// Instruments of the business metrics.
#[derive(Clone)]
pub struct DomainMetrics {
    created: Counter<u64>,
    assignments: Counter<u64>,
    reassignments: Counter<u64>,
    offers: Counter<u64>,
    pickups: Counter<u64>,
    completed: Counter<u64>,
    failed: Counter<u64>,
    assignment_latency: Histogram<f64>,
    duration: Histogram<f64>,
    failure_reasons: Arc<[String]>,
    now: EpochClock,
}

impl DomainMetrics {
    /// Instruments of the global MeterProvider (no-op until the pipeline
    /// starts). `failure_reasons` is the closed set of `nomnom.reason` of
    /// `nomnom.delivery.failed`.
    #[must_use]
    pub fn global(failure_reasons: &[&str]) -> Self {
        Self::new(
            &opentelemetry::global::meter(METER_NAME),
            failure_reasons,
            Arc::new(system_now),
        )
    }

    #[must_use]
    pub fn new(meter: &Meter, failure_reasons: &[&str], now: EpochClock) -> Self {
        let counter = |name: &'static str, unit: &'static str, description: &'static str| {
            meter
                .u64_counter(name)
                .with_unit(unit)
                .with_description(description)
                .build()
        };
        Self {
            created: counter(
                "nomnom.delivery.created",
                "{delivery}",
                "Deliveries created.",
            ),
            assignments: counter(
                "nomnom.delivery.assignments",
                "{assignment}",
                "Courier assignments.",
            ),
            reassignments: counter(
                "nomnom.delivery.reassignments",
                "{assignment}",
                "Courier reassignments.",
            ),
            offers: counter(
                "nomnom.delivery.offers",
                "{offer}",
                "Delivery offers: accepted, rejected, expired.",
            ),
            pickups: counter("nomnom.delivery.pickups", "{delivery}", "Pickups."),
            completed: counter(
                "nomnom.delivery.completed",
                "{delivery}",
                "Deliveries completed.",
            ),
            failed: counter("nomnom.delivery.failed", "{delivery}", "Deliveries failed."),
            assignment_latency: meter
                .f64_histogram("nomnom.delivery.assignment.latency")
                .with_unit("s")
                .with_description("Delivery creation to courier assignment.")
                .with_boundaries(ASSIGNMENT_LATENCY_BUCKETS.to_vec())
                .build(),
            duration: meter
                .f64_histogram("nomnom.delivery.duration")
                .with_unit("s")
                .with_description("Delivery creation to completion.")
                .with_boundaries(DURATION_BUCKETS.to_vec())
                .build(),
            failure_reasons: failure_reasons
                .iter()
                .map(|reason| (*reason).to_owned())
                .collect(),
            now,
        }
    }

    fn record(&self, events: &[DomainEvent]) {
        for event in events {
            match event {
                DomainEvent::DeliveryCreated { .. } => self.created.add(1, &[]),
                DomainEvent::CourierAssignedToDelivery {
                    created_at_epoch_seconds,
                    ..
                } => {
                    self.assignments.add(1, &[]);
                    self.elapsed(&self.assignment_latency, *created_at_epoch_seconds);
                }
                DomainEvent::CourierUnassignedFromDelivery { .. } => self
                    .reassignments
                    .add(1, &[KeyValue::new(REASON, OTHER_REASON)]),
                DomainEvent::DeliveryOfferAccepted { .. } => {
                    self.offers.add(1, &[KeyValue::new(OUTCOME, "accepted")]);
                }
                DomainEvent::DeliveryOfferRejected { .. } => {
                    self.offers.add(1, &[KeyValue::new(OUTCOME, "rejected")]);
                }
                DomainEvent::DeliveryOfferExpired { .. } => {
                    self.offers.add(1, &[KeyValue::new(OUTCOME, "expired")]);
                }
                DomainEvent::DeliveryPickedUp { .. } => self.pickups.add(1, &[]),
                DomainEvent::DeliveryCompleted {
                    created_at_epoch_seconds,
                    ..
                } => {
                    self.completed.add(1, &[]);
                    self.elapsed(&self.duration, *created_at_epoch_seconds);
                }
                DomainEvent::DeliveryFailed { reason, .. } => self.failed.add(
                    1,
                    &[KeyValue::new(REASON, self.failure_reason(reason.as_str()))],
                ),
                DomainEvent::DeliveryOffered { .. } | DomainEvent::DeliveryCancelled { .. } => {}
            }
        }
    }

    /// Seconds since creation; a Delivery stored before its creation time was
    /// recorded has no duration.
    fn elapsed(&self, histogram: &Histogram<f64>, created_at_epoch_seconds: Option<i64>) {
        if let Some(created_at) = created_at_epoch_seconds {
            let seconds = (self.now)().saturating_sub(created_at).max(0);
            histogram.record(seconds as f64, &[]);
        }
    }

    fn failure_reason(&self, reason: &str) -> String {
        if self.failure_reasons.iter().any(|known| known == reason) {
            reason.to_owned()
        } else {
            OTHER_REASON.to_owned()
        }
    }
}

/// What the outermost Unit of Work of a thread records once it commits.
enum Observation {
    Event(DomainEvent),
    Transition(Transition),
}

/// The open Unit of Work of one thread: its observations and the stored
/// states of the aggregates it loaded, added or saved.
#[derive(Default)]
struct Scope {
    observations: Vec<Observation>,
    deliveries: HashMap<DeliveryId, State>,
    offers: HashMap<DeliveryOfferId, OfferState>,
}

/// Decorator of the Unit of Work, Outbox and repository ports of the
/// `delivery` slice that records [`DomainMetrics`] and FSM transitions after
/// the outermost commit. Use cases run synchronously on one thread, so
/// observations are collected per thread.
pub struct ObservedOutbox {
    metrics: DomainMetrics,
    transitions: TransitionMetrics,
    failure_log: Option<FailureLog>,
    pending: Mutex<HashMap<ThreadId, Scope>>,
}

impl ObservedOutbox {
    #[must_use]
    pub fn new(metrics: DomainMetrics, transitions: TransitionMetrics) -> Self {
        Self {
            metrics,
            transitions,
            failure_log: None,
            pending: Mutex::new(HashMap::new()),
        }
    }

    /// Also logs every committed `DeliveryFailed` and annotates the active span.
    #[must_use]
    pub fn with_failure_log(mut self, log: FailureLog) -> Self {
        self.failure_log = Some(log);
        self
    }

    /// Committed observations in their order: metrics and the failure log of
    /// events, transitions.
    fn observe(&self, observations: &[Observation]) {
        for observation in observations {
            match observation {
                Observation::Event(event) => {
                    self.metrics.record(std::slice::from_ref(event));
                    self.log_failure(event);
                }
                Observation::Transition(transition) => self.transitions.applied(*transition),
            }
        }
    }

    fn log_failure(&self, event: &DomainEvent) {
        let Some(log) = &self.failure_log else {
            return;
        };
        if let DomainEvent::DeliveryFailed {
            delivery_id,
            order_id,
            courier_id,
            reason,
            ..
        } = event
        {
            log.record(&ReportedFailure {
                delivery_id,
                order_id,
                courier_id: Some(courier_id),
                reason,
            });
        }
    }

    fn pending(&self) -> MutexGuard<'_, HashMap<ThreadId, Scope>> {
        self.pending.lock().unwrap_or_else(PoisonError::into_inner)
    }

    /// [`TransactionScope::run_scoped`] of `inner`, recording the events
    /// added to the Outbox and the transitions saved once the outermost scope
    /// commits.
    pub fn run_scoped(
        &self,
        inner: &dyn TransactionScope,
        work: &mut dyn FnMut() -> bool,
    ) -> Result<(), UnitOfWorkError> {
        let thread = thread::current().id();
        let nested_mark = {
            let mut pending = self.pending();
            if let Some(scope) = pending.get(&thread) {
                Some(scope.observations.len())
            } else {
                pending.insert(thread, Scope::default());
                None
            }
        };
        let mut committed = false;
        if let Some(mark) = nested_mark {
            let outcome = inner.run_scoped(&mut || {
                committed = work();
                committed
            });
            // Observations of a nested scope that rolled back are not counted.
            if (outcome.is_err() || !committed)
                && let Some(scope) = self.pending().get_mut(&thread)
            {
                scope.observations.truncate(mark);
            }
            return outcome;
        }
        let collector = Collector {
            owner: self,
            thread,
        };
        let outcome = inner.run_scoped(&mut || {
            committed = work();
            committed
        });
        let observations = collector.finish();
        if outcome.is_ok() && committed {
            self.observe(&observations);
        }
        outcome
    }

    /// [`DeliveryOutbox::add`] of `inner`. Outside a Unit of Work the write
    /// is already committed, so the event is recorded at once.
    pub fn add(
        &self,
        inner: &dyn DeliveryOutbox,
        message: OutboxMessage,
    ) -> Result<(), RepositoryError> {
        let event = message.event.clone();
        inner.add(message)?;
        let thread = thread::current().id();
        let mut pending = self.pending();
        if let Some(scope) = pending.get_mut(&thread) {
            scope.observations.push(Observation::Event(event));
        } else {
            drop(pending);
            self.observe(&[Observation::Event(event)]);
        }
        Ok(())
    }

    /// [`DeliveryRepository::get`] of `inner`.
    pub fn get_delivery(
        &self,
        inner: &dyn DeliveryRepository,
        id: &DeliveryId,
    ) -> Result<Option<Delivery>, RepositoryError> {
        let delivery = DeliveryRepository::get(inner, id)?;
        if let Some(delivery) = &delivery {
            self.stored_delivery(delivery);
        }
        Ok(delivery)
    }

    /// [`DeliveryRepository::find_by_order`] of `inner`.
    pub fn find_delivery_by_order(
        &self,
        inner: &dyn DeliveryRepository,
        order_id: &OrderId,
    ) -> Result<Option<Delivery>, RepositoryError> {
        let delivery = inner.find_by_order(order_id)?;
        if let Some(delivery) = &delivery {
            self.stored_delivery(delivery);
        }
        Ok(delivery)
    }

    /// [`DeliveryRepository::add`] of `inner`.
    pub fn add_delivery(
        &self,
        inner: &dyn DeliveryRepository,
        delivery: &Delivery,
    ) -> Result<(), RepositoryError> {
        DeliveryRepository::add(inner, delivery)?;
        self.stored_delivery(delivery);
        Ok(())
    }

    /// [`DeliveryRepository::save`] of `inner`; a changed state is the applied
    /// transition.
    pub fn save_delivery(
        &self,
        inner: &dyn DeliveryRepository,
        delivery: &Delivery,
    ) -> Result<(), RepositoryError> {
        DeliveryRepository::save(inner, delivery)?;
        self.stored_delivery(delivery);
        Ok(())
    }

    /// [`DeliveryOfferRepository::get`] of `inner`.
    pub fn get_offer(
        &self,
        inner: &dyn DeliveryOfferRepository,
        id: &DeliveryOfferId,
    ) -> Result<Option<DeliveryOffer>, RepositoryError> {
        let offer = DeliveryOfferRepository::get(inner, id)?;
        if let Some(offer) = &offer {
            self.stored_offer(offer);
        }
        Ok(offer)
    }

    /// [`DeliveryOfferRepository::add`] of `inner`.
    pub fn add_offer(
        &self,
        inner: &dyn DeliveryOfferRepository,
        offer: &DeliveryOffer,
    ) -> Result<(), RepositoryError> {
        DeliveryOfferRepository::add(inner, offer)?;
        self.stored_offer(offer);
        Ok(())
    }

    /// [`DeliveryOfferRepository::save`] of `inner`; a changed state is the
    /// applied transition.
    pub fn save_offer(
        &self,
        inner: &dyn DeliveryOfferRepository,
        offer: &DeliveryOffer,
    ) -> Result<(), RepositoryError> {
        DeliveryOfferRepository::save(inner, offer)?;
        self.stored_offer(offer);
        Ok(())
    }

    /// Remembers the stored state of `delivery` inside the open Unit of
    /// Work; a different state stored before it is the applied transition
    /// between them. Outside a Unit of Work nothing is tracked.
    fn stored_delivery(&self, delivery: &Delivery) {
        let mut pending = self.pending();
        let Some(scope) = pending.get_mut(&thread::current().id()) else {
            return;
        };
        let to = delivery.state();
        if let Some(from) = scope.deliveries.insert(delivery.id().clone(), to)
            && let Some(result) = applied_transition(from, to)
        {
            scope
                .observations
                .push(Observation::Transition(Transition::Delivery(result)));
        }
    }

    /// [`Self::stored_delivery`] of a Delivery Offer.
    fn stored_offer(&self, offer: &DeliveryOffer) {
        let mut pending = self.pending();
        let Some(scope) = pending.get_mut(&thread::current().id()) else {
            return;
        };
        let to = offer.state();
        if let Some(from) = scope.offers.insert(offer.id().clone(), to)
            && let Some(result) = applied_offer_transition(from, to)
        {
            scope
                .observations
                .push(Observation::Transition(Transition::Offer(result)));
        }
    }
}

/// Observations of the outermost scope of one thread; dropped on panic too,
/// so a failed Unit of Work never leaks them into the next one.
struct Collector<'a> {
    owner: &'a ObservedOutbox,
    thread: ThreadId,
}

impl Collector<'_> {
    fn finish(self) -> Vec<Observation> {
        self.owner
            .pending()
            .remove(&self.thread)
            .map(|scope| scope.observations)
            .unwrap_or_default()
    }
}

impl Drop for Collector<'_> {
    fn drop(&mut self) {
        self.owner.pending().remove(&self.thread);
    }
}

#[cfg(test)]
#[path = "observability/tests.rs"]
mod tests;

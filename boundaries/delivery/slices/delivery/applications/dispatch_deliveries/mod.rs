//! Dispatch Deliveries: the background job of the `delivery` process starts
//! the courier search of waiting Deliveries (FR-582). One call handles one
//! batch: waiting deliveries without a pending offer in creation order,
//! after the given position, at most `batch_size`. Every delivery is
//! dispatched in its own Unit of Work: Find Courier ([`select_candidate`])
//! excludes couriers who were already offered the delivery, and Offer
//! Delivery ([`make_offer`]) offers it to the candidate with its
//! `DeliveryOffered` fact in the Outbox. A delivery without a candidate stays
//! waiting for the next pass.
//!
//! The delivery is saved with the version it was read at before the offer
//! is made, so a concurrent change of it — another dispatcher, an accepted
//! offer, Replan or Cancel Delivery — is an optimistic concurrency conflict:
//! the delivery is skipped, not an error. So is a courier that stopped being
//! available after it was selected.
//!
//! The job runs inside the process, not on behalf of a caller, so there is no
//! subject and no permission check.

use crate::pkg::application::{Clock, IdGenerator, UnitOfWork};
use crate::pkg::persistence::RepositoryError;
use crate::slices::courier::applications::assignment::CourierAssignment;
use crate::slices::delivery::applications::offers_of_delivery::OffersOfDelivery;
use crate::slices::delivery::domains::delivery::{
    Delivery, DeliveryId, DeliveryOfferRepository, DeliveryRepository,
};

use super::courier_locator::CourierLocator;
use super::error::DeliveryApplicationError;
use super::find_courier::{Outcome, select_candidate};
use super::offer_delivery::{self, OfferPorts, make_offer};
use super::outbox::DeliveryOutbox;
use super::policy::AssignmentPolicy;

/// Place of a delivery in creation order: creation time, then id. A Delivery
/// stored before its creation time was recorded counts as created at `0`.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct DeliveryPosition {
    pub created_at_epoch_seconds: i64,
    pub delivery_id: DeliveryId,
}

impl DeliveryPosition {
    #[must_use]
    pub fn of(delivery: &Delivery) -> Self {
        Self {
            created_at_epoch_seconds: delivery.created_at_epoch_seconds().unwrap_or(0),
            delivery_id: delivery.id().clone(),
        }
    }

    /// Sort key of the order: ids compare by bytes, as the PostgreSQL
    /// `"C"` collation does.
    #[must_use]
    pub fn key(&self) -> (i64, &str) {
        (self.created_at_epoch_seconds, self.delivery_id.as_str())
    }
}

/// Query of the deliveries to dispatch. Implemented only by the PostgreSQL
/// store, which filters and pages in SQL.
pub trait WaitingDeliveries {
    /// Waiting deliveries without a pending offer in creation order
    /// ([`DeliveryPosition`]), strictly after `after` when given, at most
    /// `limit`.
    fn list_waiting_without_pending_offer(
        &self,
        after: Option<&DeliveryPosition>,
        limit: usize,
    ) -> Result<Vec<Delivery>, RepositoryError>;
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct Input {
    /// Maximum number of deliveries handled by this call; `0` handles none.
    pub batch_size: usize,
    /// Position of the last delivery of the previous batch of the same pass.
    pub after: Option<DeliveryPosition>,
}

#[derive(Clone, Debug, Default, Eq, PartialEq)]
pub struct Output {
    /// Offers this call made, in the order handled.
    pub offered: Vec<offer_delivery::Output>,
    /// Deliveries left waiting: no available courier who was not offered
    /// them yet.
    pub without_candidate: Vec<DeliveryId>,
    /// Deliveries or couriers changed concurrently; left as they are.
    pub skipped: Vec<DeliveryId>,
    /// Position of the last delivery when the batch was full, so more
    /// waiting deliveries may follow it; `None` otherwise.
    pub next: Option<DeliveryPosition>,
}

/// Outcome of one delivery of the batch.
enum Dispatch {
    Offered(offer_delivery::Output),
    WithoutCandidate,
    Skipped,
}

pub struct Handler<'a, U: UnitOfWork> {
    pub unit_of_work: &'a U,
    pub waiting: &'a dyn WaitingDeliveries,
    pub deliveries: &'a dyn DeliveryRepository,
    pub couriers: &'a dyn CourierAssignment,
    pub offers: &'a dyn DeliveryOfferRepository,
    pub offers_of: &'a dyn OffersOfDelivery,
    pub outbox: &'a dyn DeliveryOutbox,
    pub locator: &'a dyn CourierLocator,
    pub ids: &'a dyn IdGenerator,
    pub clock: &'a dyn Clock,
    pub policy: AssignmentPolicy,
}

impl<U: UnitOfWork> Handler<'_, U> {
    pub fn execute(&self, input: Input) -> Result<Output, DeliveryApplicationError> {
        let mut output = Output::default();
        if input.batch_size == 0 {
            return Ok(output);
        }
        let batch = self
            .waiting
            .list_waiting_without_pending_offer(input.after.as_ref(), input.batch_size)?;
        if batch.len() >= input.batch_size {
            output.next = batch.last().map(DeliveryPosition::of);
        }
        for delivery in batch {
            let id = delivery.id().clone();
            match self.dispatch(&delivery)? {
                Dispatch::Offered(offer) => output.offered.push(offer),
                Dispatch::WithoutCandidate => output.without_candidate.push(id),
                Dispatch::Skipped => output.skipped.push(id),
            }
        }
        Ok(output)
    }

    fn dispatch(&self, delivery: &Delivery) -> Result<Dispatch, DeliveryApplicationError> {
        let outcome = self.unit_of_work.run(|| {
            let selection = select_candidate(
                delivery,
                self.offers_of,
                self.offers,
                self.couriers,
                self.locator,
                self.policy,
            )?;
            let Outcome::CandidateFound { courier_id, .. } = selection.outcome else {
                return Ok(Dispatch::WithoutCandidate);
            };
            // Claims the delivery at the version of the batch snapshot.
            self.deliveries.save(delivery)?;
            let offer = make_offer(
                delivery,
                courier_id,
                &OfferPorts {
                    couriers: self.couriers,
                    offers: self.offers,
                    offers_of: self.offers_of,
                    outbox: self.outbox,
                    ids: self.ids,
                    clock: self.clock,
                    policy: self.policy,
                },
            )?;
            Ok::<_, DeliveryApplicationError>(Dispatch::Offered(offer))
        });
        match outcome {
            // The delivery or the selected courier changed after the batch
            // was read; the next pass sees the new state.
            Err(
                DeliveryApplicationError::ConcurrentModification
                | DeliveryApplicationError::CourierPrecondition(_),
            ) => Ok(Dispatch::Skipped),
            outcome => outcome,
        }
    }
}

#[cfg(test)]
mod tests;

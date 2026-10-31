//! Record Delivery Duration: the consumer of the Delivery's own facts keeps
//! the Delivery Duration Statistics of the Market (FR-676), like Wait Time
//! Statistics of `restaurants`. One fact is processed once (Transactional
//! Inbox, ADR 11): the Inbox record and the statistics change commit in one
//! Unit of Work, a repeated `event_id` changes nothing.
//!
//! | Fact | Change |
//! | --- | --- |
//! | Pickup | `courier_lead` = Pickup − creation of the Delivery; the transit of the Delivery opens at Pickup |
//! | Completed | `transit` = handover − Pickup of the open transit, which closes |
//! | Cancelled, Failed | the open transit closes without a duration |
//!
//! A Delivery stored before its creation time was recorded has no
//! `courier_lead`; a handover without an open transit (picked up before the
//! statistics existed) has no `transit`. Facts of one Delivery arrive in
//! order: the stream is keyed by the Delivery id.

use std::time::Duration;

use crate::pkg::persistence::RepositoryError;
use crate::pkg::unit_of_work::UnitOfWork;
use crate::slices::courier::domains::courier::MarketId;
use crate::slices::delivery::domains::delivery::{
    DeliveryEstimatePolicy, DeliveryId, DeliveryRepository, DurationKind,
};

use super::super::error::DeliveryApplicationError;
use super::super::{delivery_id, identifier, load_delivery};

/// Name of the consumer in the Inbox.
pub const CONSUMER: &str = "delivery.duration-statistics";

/// Transactional Inbox of the consumers of the process (ADR 11). Writes
/// participate in the active Unit of Work.
pub trait DeliveryInbox {
    /// Records `event_id` as processed by `consumer`; `false` when it already
    /// was.
    fn claim(
        &self,
        consumer: &str,
        event_id: &str,
        event_type: &str,
    ) -> Result<bool, RepositoryError>;
}

/// Transit of a picked up Delivery that is not handed over yet.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct OpenTransit {
    pub market_id: MarketId,
    /// Pickup, epoch milliseconds.
    pub picked_up_at_ms: i64,
}

/// Delivery Duration Statistics of the Markets. Writes participate in the
/// active Unit of Work.
pub trait DeliveryDurationStatistics {
    /// Opens the transit of the Delivery at Pickup; an open one stays.
    fn open_transit(
        &self,
        delivery_id: &DeliveryId,
        market_id: &MarketId,
        picked_up_at_ms: i64,
    ) -> Result<(), RepositoryError>;

    /// Removes the open transit of the Delivery and returns it.
    fn close_transit(
        &self,
        delivery_id: &DeliveryId,
    ) -> Result<Option<OpenTransit>, RepositoryError>;

    /// Adds the duration of a leg of the Delivery to the sample of its
    /// Market and keeps the latest `keep` durations of that leg; a second
    /// duration of the same leg of the Delivery is ignored.
    fn record_duration(
        &self,
        market_id: &MarketId,
        kind: DurationKind,
        delivery_id: &DeliveryId,
        duration: Duration,
        keep: u32,
    ) -> Result<(), RepositoryError>;
}

/// Fact of the Delivery the statistics react to.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum Fact {
    PickedUp,
    Completed,
    /// Cancelled or failed: the Delivery leaves without a duration.
    Dropped,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct Input {
    /// `event_id` of the Integration Event.
    pub event_id: String,
    pub event_type: String,
    /// `occurred_at` of the event, epoch milliseconds.
    pub occurred_at_ms: i64,
    pub delivery_id: String,
    pub fact: Fact,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum Output {
    Recorded,
    /// The event was already processed.
    Duplicate,
}

pub struct Handler<'a, U: UnitOfWork> {
    pub unit_of_work: &'a U,
    pub inbox: &'a dyn DeliveryInbox,
    pub statistics: &'a dyn DeliveryDurationStatistics,
    pub deliveries: &'a dyn DeliveryRepository,
    pub policy: DeliveryEstimatePolicy,
}

impl<U: UnitOfWork> Handler<'_, U> {
    /// Background consumer of the process: no caller subject.
    pub fn execute(&self, input: Input) -> Result<Output, DeliveryApplicationError> {
        let event_id = identifier(&input.event_id, "event_id")?;
        let delivery_id = delivery_id(&input.delivery_id)?;
        let at = input.occurred_at_ms;
        self.unit_of_work.run(|| {
            if !self.inbox.claim(CONSUMER, &event_id, &input.event_type)? {
                return Ok(Output::Duplicate);
            }
            match input.fact {
                Fact::PickedUp => self.picked_up(&delivery_id, at)?,
                Fact::Completed => self.completed(&delivery_id, at)?,
                Fact::Dropped => {
                    self.statistics.close_transit(&delivery_id)?;
                }
            }
            Ok(Output::Recorded)
        })
    }

    fn picked_up(
        &self,
        delivery_id: &DeliveryId,
        picked_up_at_ms: i64,
    ) -> Result<(), DeliveryApplicationError> {
        let delivery = load_delivery(self.deliveries, delivery_id)?;
        let lead = delivery
            .created_at_epoch_seconds()
            .and_then(|created| elapsed(created.saturating_mul(1000), picked_up_at_ms));
        if let Some(lead) = lead {
            self.record(
                delivery.market_id(),
                DurationKind::CourierLead,
                delivery_id,
                lead,
            )?;
        }
        self.statistics
            .open_transit(delivery_id, delivery.market_id(), picked_up_at_ms)?;
        Ok(())
    }

    fn completed(
        &self,
        delivery_id: &DeliveryId,
        delivered_at_ms: i64,
    ) -> Result<(), DeliveryApplicationError> {
        let Some(open) = self.statistics.close_transit(delivery_id)? else {
            return Ok(());
        };
        if let Some(transit) = elapsed(open.picked_up_at_ms, delivered_at_ms) {
            self.record(&open.market_id, DurationKind::Transit, delivery_id, transit)?;
        }
        Ok(())
    }

    fn record(
        &self,
        market_id: &MarketId,
        kind: DurationKind,
        delivery_id: &DeliveryId,
        duration: Duration,
    ) -> Result<(), RepositoryError> {
        self.statistics.record_duration(
            market_id,
            kind,
            delivery_id,
            duration,
            self.policy.sample_size(),
        )
    }
}

/// Time from `from_ms` to `to_ms`; a negative span (clocks of the process
/// and the database apart) is not a duration and is not recorded.
fn elapsed(from_ms: i64, to_ms: i64) -> Option<Duration> {
    let span = to_ms.checked_sub(from_ms)?;
    u64::try_from(span).ok().map(Duration::from_millis)
}

#[cfg(test)]
mod tests;

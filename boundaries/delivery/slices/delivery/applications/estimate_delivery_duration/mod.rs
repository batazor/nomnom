//! Estimate Delivery Duration: the fulfillment process of `orders` reads the
//! expected `courier_lead` (Create Delivery → Pickup) and `transit` (Pickup →
//! handover) of a Market to plan a scheduled order (FR-691). Each leg is the
//! median of the latest `sample_size` durations of the Market in the
//! Delivery Duration Statistics, its baseline until the sample is full. The
//! right is the capability of the fulfillment commands
//! (`platform#coordinate_delivery`).

use std::time::Duration;

use crate::pkg::application::Subject;
use crate::pkg::persistence::RepositoryError;
use crate::slices::courier::domains::courier::MarketId;
use crate::slices::delivery::domains::delivery::{
    DeliveryDurationEstimate, DeliveryEstimatePolicy, DurationKind,
};

use super::authorization::{DeliveryAuthorization, DeliveryPermission, DeliveryResource};
use super::error::DeliveryApplicationError;
use super::market_id;

/// Query model of the Delivery Duration Statistics.
pub trait DeliveryDurationSamples {
    /// The latest durations of `kind` in `market_id`, at most `limit`.
    fn latest_durations(
        &self,
        market_id: &MarketId,
        kind: DurationKind,
        limit: u32,
    ) -> Result<Vec<Duration>, RepositoryError>;
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct Input {
    /// Market of the estimate; required.
    pub market_id: String,
}

pub type Output = DeliveryDurationEstimate;

pub struct Handler<'a> {
    pub authorization: &'a dyn DeliveryAuthorization,
    pub samples: &'a dyn DeliveryDurationSamples,
    pub policy: DeliveryEstimatePolicy,
}

impl Handler<'_> {
    pub fn execute(
        &self,
        subject: &Subject,
        input: Input,
    ) -> Result<Output, DeliveryApplicationError> {
        let market_id = market_id(&input.market_id)?;
        self.authorization.check(
            subject,
            DeliveryPermission::EstimateDeliveryDuration,
            &DeliveryResource::Platform,
        )?;
        Ok(DeliveryDurationEstimate {
            courier_lead: self.estimate(&market_id, DurationKind::CourierLead)?,
            transit: self.estimate(&market_id, DurationKind::Transit)?,
        })
    }

    fn estimate(
        &self,
        market_id: &MarketId,
        kind: DurationKind,
    ) -> Result<Duration, DeliveryApplicationError> {
        let latest = self
            .samples
            .latest_durations(market_id, kind, self.policy.sample_size())?;
        Ok(self.policy.estimate(kind, &latest))
    }
}

#[cfg(test)]
mod tests;

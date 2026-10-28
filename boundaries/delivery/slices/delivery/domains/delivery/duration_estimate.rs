//! Delivery Duration Statistics (FR-676, FR-691): a read model built from the
//! Delivery's own facts, not part of the Delivery aggregate. Per Market it
//! keeps the latest durations of two legs:
//!
//! - `courier_lead` — Create Delivery → Pickup;
//! - `transit` — Pickup → handover.
//!
//! The estimate of a leg is the median of its latest `sample_size`
//! durations; until the Market has that many, it is the configured baseline
//! of the leg. The Market is the only key: a Delivery carries no addresses.

use std::error::Error;
use std::fmt::{Display, Formatter};
use std::time::Duration;

/// Leg of a Delivery measured by the statistics.
#[derive(Clone, Copy, Debug, Eq, Hash, PartialEq)]
pub enum DurationKind {
    /// Create Delivery → Pickup.
    CourierLead,
    /// Pickup → handover.
    Transit,
}

impl DurationKind {
    pub const ALL: [Self; 2] = [Self::CourierLead, Self::Transit];

    /// Stored name of the leg.
    #[must_use]
    pub const fn as_str(self) -> &'static str {
        match self {
            Self::CourierLead => "courier_lead",
            Self::Transit => "transit",
        }
    }
}

/// Invalid parameter of [`DeliveryEstimatePolicy`]; the process does not
/// start with it.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum InvalidEstimatePolicy {
    /// The sample size is zero.
    SampleSize,
    /// The baseline of the leg is zero.
    Baseline(DurationKind),
}

impl Display for InvalidEstimatePolicy {
    fn fmt(&self, formatter: &mut Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::SampleSize => formatter.write_str("sample size must be positive"),
            Self::Baseline(kind) => {
                write!(formatter, "baseline {} must be positive", kind.as_str())
            }
        }
    }
}

impl Error for InvalidEstimatePolicy {}

/// Estimate of both legs of a Delivery in one Market.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct DeliveryDurationEstimate {
    pub courier_lead: Duration,
    pub transit: Duration,
}

/// Parameters of the estimate: the sample size and the baseline of each leg
/// until its sample is full.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct DeliveryEstimatePolicy {
    sample_size: u32,
    baseline_courier_lead: Duration,
    baseline_transit: Duration,
}

impl DeliveryEstimatePolicy {
    pub fn new(
        sample_size: u32,
        baseline_courier_lead: Duration,
        baseline_transit: Duration,
    ) -> Result<Self, InvalidEstimatePolicy> {
        if sample_size == 0 {
            return Err(InvalidEstimatePolicy::SampleSize);
        }
        if baseline_courier_lead.is_zero() {
            return Err(InvalidEstimatePolicy::Baseline(DurationKind::CourierLead));
        }
        if baseline_transit.is_zero() {
            return Err(InvalidEstimatePolicy::Baseline(DurationKind::Transit));
        }
        Ok(Self {
            sample_size,
            baseline_courier_lead,
            baseline_transit,
        })
    }

    /// Durations of a leg the statistics keep per Market.
    #[must_use]
    pub const fn sample_size(&self) -> u32 {
        self.sample_size
    }

    #[must_use]
    pub const fn baseline(&self, kind: DurationKind) -> Duration {
        match kind {
            DurationKind::CourierLead => self.baseline_courier_lead,
            DurationKind::Transit => self.baseline_transit,
        }
    }

    /// Estimate of a leg from its latest durations in a Market, at most
    /// `sample_size` of them: their median once the sample is full, the
    /// baseline before.
    #[must_use]
    pub fn estimate(&self, kind: DurationKind, latest: &[Duration]) -> Duration {
        if latest.len() < self.sample_size as usize {
            return self.baseline(kind);
        }
        median(latest)
    }
}

/// Median of a non-empty sample; the mean of the two middle values for an
/// even count.
fn median(values: &[Duration]) -> Duration {
    let mut sorted = values.to_vec();
    sorted.sort_unstable();
    let middle = sorted.len() / 2;
    if sorted.len() % 2 == 1 {
        sorted[middle]
    } else {
        (sorted[middle - 1] + sorted[middle]) / 2
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn minutes(value: u64) -> Duration {
        Duration::from_secs(value * 60)
    }

    fn policy(sample_size: u32) -> DeliveryEstimatePolicy {
        DeliveryEstimatePolicy::new(sample_size, minutes(15), minutes(20)).expect("valid policy")
    }

    /// AC-904
    #[test]
    fn full_sample_gives_its_median() {
        let latest = [
            minutes(30),
            minutes(18),
            minutes(22),
            minutes(25),
            minutes(20),
        ];

        assert_eq!(
            policy(5).estimate(DurationKind::Transit, &latest),
            minutes(22)
        );
    }

    #[test]
    fn even_sample_gives_the_mean_of_the_middle_values() {
        let latest = [minutes(10), minutes(14), minutes(12), minutes(20)];

        assert_eq!(
            policy(4).estimate(DurationKind::CourierLead, &latest),
            minutes(13)
        );
    }

    /// AC-905
    #[test]
    fn incomplete_sample_gives_the_baseline_of_the_leg() {
        let latest = [minutes(40), minutes(41)];

        assert_eq!(
            policy(3).estimate(DurationKind::CourierLead, &latest),
            minutes(15)
        );
        assert_eq!(policy(3).estimate(DurationKind::Transit, &[]), minutes(20));
    }

    #[test]
    fn zero_parameters_are_rejected() {
        assert_eq!(
            DeliveryEstimatePolicy::new(0, minutes(15), minutes(20)),
            Err(InvalidEstimatePolicy::SampleSize)
        );
        assert_eq!(
            DeliveryEstimatePolicy::new(50, Duration::ZERO, minutes(20)),
            Err(InvalidEstimatePolicy::Baseline(DurationKind::CourierLead))
        );
        assert_eq!(
            DeliveryEstimatePolicy::new(50, minutes(15), Duration::ZERO),
            Err(InvalidEstimatePolicy::Baseline(DurationKind::Transit))
        );
    }
}

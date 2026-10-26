use std::error::Error;
use std::fmt::{Display, Formatter};

use crate::pkg::persistence::AuthorizationRevision;

use super::{CourierName, MarketId};

#[derive(Clone, Debug, Eq, Hash, Ord, PartialEq, PartialOrd)]
pub struct CourierId(String);

impl CourierId {
    #[must_use]
    pub fn new(value: impl Into<String>) -> Self {
        Self(value.into())
    }

    #[must_use]
    pub fn as_str(&self) -> &str {
        &self.0
    }
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum CourierStatus {
    /// Accepts new Delivery Offers and Courier Assignments.
    Available,
    /// Holds a Courier Assignment.
    Busy,
    /// Off shift: does not accept new Delivery Offers or Courier Assignments.
    Unavailable,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum CourierError {
    AlreadyAvailable,
    AlreadyBusy,
    AlreadyUnavailable,
    /// The courier is unavailable and cannot take a new assignment.
    Unavailable,
    /// The courier holds an assignment and its availability cannot change.
    Busy,
    /// The courier holds no assignment that could be released.
    NotBusy,
    /// The courier is on line (available or busy) and cannot retire.
    OnLine,
    /// The courier left the Courier Pool and takes no work any more.
    Retired,
    AlreadyRetired,
    /// The work belongs to another Market than the courier's (ADR 41).
    OtherMarket,
}

impl Display for CourierError {
    fn fmt(&self, formatter: &mut Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::AlreadyAvailable => formatter.write_str("courier is already available"),
            Self::AlreadyBusy => formatter.write_str("courier is already busy"),
            Self::AlreadyUnavailable => formatter.write_str("courier is already unavailable"),
            Self::Unavailable => formatter.write_str("courier is unavailable"),
            Self::Busy => formatter.write_str("courier is busy with a delivery"),
            Self::NotBusy => formatter.write_str("courier has no assignment to release"),
            Self::OnLine => formatter.write_str("courier is on line"),
            Self::Retired => formatter.write_str("courier is retired"),
            Self::AlreadyRetired => formatter.write_str("courier is already retired"),
            Self::OtherMarket => formatter.write_str("courier works in another market"),
        }
    }
}

impl Error for CourierError {}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct Courier {
    id: CourierId,
    name: CourierName,
    /// Market the courier works in; set on registration, immutable (ADR 41).
    market_id: MarketId,
    status: CourierStatus,
    /// Set once the courier left the Courier Pool; the courier stays
    /// unavailable for good.
    retired_at_epoch_seconds: Option<i64>,
    /// Technical field: revision of the last `courier:<id>#self` write.
    authorization_revision: Option<AuthorizationRevision>,
    /// Technical field: optimistic concurrency version of the stored snapshot.
    version: u64,
}

impl Courier {
    #[must_use]
    pub fn new(id: CourierId, name: CourierName, market_id: MarketId) -> Self {
        Self {
            id,
            name,
            market_id,
            status: CourierStatus::Available,
            retired_at_epoch_seconds: None,
            authorization_revision: None,
            version: 0,
        }
    }

    /// Restores a courier from a snapshot; old snapshots carry no
    /// authorization revision (see [`Self::with_authorization_revision`]).
    #[must_use]
    pub fn restore(
        id: CourierId,
        name: CourierName,
        market_id: MarketId,
        status: CourierStatus,
    ) -> Self {
        Self {
            id,
            name,
            market_id,
            status,
            retired_at_epoch_seconds: None,
            authorization_revision: None,
            version: 0,
        }
    }

    /// Snapshot field `retired_at`: the moment the courier left the pool.
    #[must_use]
    pub fn with_retired_at(mut self, retired_at_epoch_seconds: Option<i64>) -> Self {
        self.retired_at_epoch_seconds = retired_at_epoch_seconds;
        self
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

    /// Snapshot field `authorization_revision`, absent in old snapshots.
    #[must_use]
    pub fn with_authorization_revision(mut self, revision: Option<AuthorizationRevision>) -> Self {
        self.authorization_revision = revision;
        self
    }

    /// Stores the revision of a relationship write made for this courier.
    pub fn record_authorization_revision(&mut self, revision: AuthorizationRevision) {
        self.authorization_revision = Some(revision);
    }

    #[must_use]
    pub fn authorization_revision(&self) -> Option<&AuthorizationRevision> {
        self.authorization_revision.as_ref()
    }

    /// Courier Assignment rule of ADR 41: a courier takes Delivery Offers and
    /// Courier Assignments of its own Market only.
    pub fn ensure_serves(&self, market_id: &MarketId) -> Result<(), CourierError> {
        if &self.market_id == market_id {
            Ok(())
        } else {
            Err(CourierError::OtherMarket)
        }
    }

    /// Checks that the courier may receive a new Delivery Offer or Assignment
    /// without changing its state.
    pub fn ensure_available(&self) -> Result<(), CourierError> {
        match self.status {
            CourierStatus::Available => Ok(()),
            CourierStatus::Busy => Err(CourierError::AlreadyBusy),
            CourierStatus::Unavailable => Err(CourierError::Unavailable),
        }
    }

    pub fn mark_busy(&mut self) -> Result<(), CourierError> {
        self.ensure_available()?;
        self.status = CourierStatus::Busy;
        Ok(())
    }

    pub fn release(&mut self) -> Result<(), CourierError> {
        match self.status {
            CourierStatus::Busy => {
                self.status = CourierStatus::Available;
                Ok(())
            }
            CourierStatus::Available => Err(CourierError::AlreadyAvailable),
            CourierStatus::Unavailable => Err(CourierError::NotBusy),
        }
    }

    /// Returns an unavailable courier to the pool of new assignments.
    pub fn mark_available(&mut self) -> Result<(), CourierError> {
        if self.is_retired() {
            return Err(CourierError::Retired);
        }
        match self.status {
            CourierStatus::Unavailable => {
                self.status = CourierStatus::Available;
                Ok(())
            }
            CourierStatus::Available => Err(CourierError::AlreadyAvailable),
            CourierStatus::Busy => Err(CourierError::Busy),
        }
    }

    /// Excludes an available courier from new assignments.
    pub fn mark_unavailable(&mut self) -> Result<(), CourierError> {
        match self.status {
            CourierStatus::Available => {
                self.status = CourierStatus::Unavailable;
                Ok(())
            }
            CourierStatus::Unavailable => Err(CourierError::AlreadyUnavailable),
            CourierStatus::Busy => Err(CourierError::Busy),
        }
    }

    /// Leaves the Courier Pool for good; only off line (FR-619).
    pub fn retire(&mut self, at_epoch_seconds: i64) -> Result<(), CourierError> {
        if self.is_retired() {
            return Err(CourierError::AlreadyRetired);
        }
        match self.status {
            CourierStatus::Unavailable => {
                self.retired_at_epoch_seconds = Some(at_epoch_seconds);
                Ok(())
            }
            CourierStatus::Available | CourierStatus::Busy => Err(CourierError::OnLine),
        }
    }

    #[must_use]
    pub fn id(&self) -> &CourierId {
        &self.id
    }

    #[must_use]
    pub fn name(&self) -> &CourierName {
        &self.name
    }

    #[must_use]
    pub fn market_id(&self) -> &MarketId {
        &self.market_id
    }

    #[must_use]
    pub const fn retired_at_epoch_seconds(&self) -> Option<i64> {
        self.retired_at_epoch_seconds
    }

    #[must_use]
    pub const fn is_retired(&self) -> bool {
        self.retired_at_epoch_seconds.is_some()
    }

    #[must_use]
    pub const fn status(&self) -> CourierStatus {
        self.status
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn name() -> CourierName {
        CourierName::new("Timur").expect("courier name")
    }

    fn berlin() -> MarketId {
        MarketId::new("berlin").expect("market id")
    }

    #[test]
    fn courier_availability_changes_strictly() {
        let mut courier = Courier::new(CourierId::new("courier-1"), name(), berlin());

        courier.mark_busy().expect("available courier becomes busy");
        assert_eq!(courier.status(), CourierStatus::Busy);
        assert_eq!(courier.mark_busy(), Err(CourierError::AlreadyBusy));

        courier.release().expect("busy courier becomes available");
        assert_eq!(courier.status(), CourierStatus::Available);
        assert_eq!(courier.release(), Err(CourierError::AlreadyAvailable));
    }

    #[test]
    fn courier_goes_off_and_on_shift_strictly() {
        let mut courier = Courier::new(CourierId::new("courier-1"), name(), berlin());

        assert_eq!(
            courier.mark_available(),
            Err(CourierError::AlreadyAvailable)
        );
        courier
            .mark_unavailable()
            .expect("available courier goes off shift");
        assert_eq!(courier.status(), CourierStatus::Unavailable);
        assert_eq!(
            courier.mark_unavailable(),
            Err(CourierError::AlreadyUnavailable)
        );
        assert_eq!(courier.ensure_available(), Err(CourierError::Unavailable));
        assert_eq!(courier.mark_busy(), Err(CourierError::Unavailable));
        assert_eq!(courier.release(), Err(CourierError::NotBusy));

        courier
            .mark_available()
            .expect("unavailable courier returns");
        assert_eq!(courier.status(), CourierStatus::Available);
    }

    #[test]
    fn busy_courier_availability_cannot_change() {
        let mut courier = Courier::restore(
            CourierId::new("courier-1"),
            name(),
            berlin(),
            CourierStatus::Busy,
        );

        assert_eq!(courier.mark_unavailable(), Err(CourierError::Busy));
        assert_eq!(courier.mark_available(), Err(CourierError::Busy));
        assert_eq!(courier.ensure_available(), Err(CourierError::AlreadyBusy));
        assert_eq!(courier.status(), CourierStatus::Busy);
    }

    /// AC-788, AC-789
    #[test]
    fn only_a_courier_off_line_retires_for_good() {
        let mut courier = Courier::new(CourierId::new("courier-1"), name(), berlin());

        assert_eq!(courier.retire(10), Err(CourierError::OnLine));
        courier.mark_busy().expect("assigned");
        assert_eq!(courier.retire(10), Err(CourierError::OnLine));
        courier.release().expect("released");
        courier.mark_unavailable().expect("off line");
        courier.retire(10).expect("retired");

        assert!(courier.is_retired());
        assert_eq!(courier.retired_at_epoch_seconds(), Some(10));
        assert_eq!(courier.retire(20), Err(CourierError::AlreadyRetired));
        assert_eq!(courier.mark_available(), Err(CourierError::Retired));
        assert_eq!(courier.ensure_available(), Err(CourierError::Unavailable));
    }

    /// ADR 41: a courier serves only its own Market.
    #[test]
    fn courier_serves_only_its_market() {
        let courier = Courier::new(CourierId::new("courier-1"), name(), berlin());

        assert_eq!(courier.market_id(), &berlin());
        assert_eq!(courier.ensure_serves(&berlin()), Ok(()));
        assert_eq!(
            courier.ensure_serves(&MarketId::new("hamburg").expect("market id")),
            Err(CourierError::OtherMarket)
        );
    }
}

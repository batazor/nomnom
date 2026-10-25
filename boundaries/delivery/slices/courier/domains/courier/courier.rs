use std::error::Error;
use std::fmt::{Display, Formatter};

use crate::pkg::persistence::AuthorizationRevision;

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
        }
    }
}

impl Error for CourierError {}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct Courier {
    id: CourierId,
    status: CourierStatus,
    /// Technical field: revision of the last `courier:<id>#self` write.
    authorization_revision: Option<AuthorizationRevision>,
}

impl Courier {
    #[must_use]
    pub fn new(id: CourierId) -> Self {
        Self {
            id,
            status: CourierStatus::Available,
            authorization_revision: None,
        }
    }

    /// Restores a courier from a snapshot; old snapshots carry no
    /// authorization revision (see [`Self::with_authorization_revision`]).
    #[must_use]
    pub fn restore(id: CourierId, status: CourierStatus) -> Self {
        Self {
            id,
            status,
            authorization_revision: None,
        }
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

    #[must_use]
    pub fn id(&self) -> &CourierId {
        &self.id
    }

    #[must_use]
    pub const fn status(&self) -> CourierStatus {
        self.status
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn courier_availability_changes_strictly() {
        let mut courier = Courier::new(CourierId::new("courier-1"));

        courier.mark_busy().expect("available courier becomes busy");
        assert_eq!(courier.status(), CourierStatus::Busy);
        assert_eq!(courier.mark_busy(), Err(CourierError::AlreadyBusy));

        courier.release().expect("busy courier becomes available");
        assert_eq!(courier.status(), CourierStatus::Available);
        assert_eq!(courier.release(), Err(CourierError::AlreadyAvailable));
    }

    #[test]
    fn courier_goes_off_and_on_shift_strictly() {
        let mut courier = Courier::new(CourierId::new("courier-1"));

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
        let mut courier = Courier::restore(CourierId::new("courier-1"), CourierStatus::Busy);

        assert_eq!(courier.mark_unavailable(), Err(CourierError::Busy));
        assert_eq!(courier.mark_available(), Err(CourierError::Busy));
        assert_eq!(courier.ensure_available(), Err(CourierError::AlreadyBusy));
        assert_eq!(courier.status(), CourierStatus::Busy);
    }
}

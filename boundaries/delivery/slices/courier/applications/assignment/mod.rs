//! Courier busyness port of the `courier` slice. The `delivery` slice checks
//! and changes whether a courier is busy only through [`CourierAssignment`];
//! availability rules stay in the `Courier` aggregate.
//!
//! The implementation works on top of [`CourierRepository`]: it loads the
//! aggregate, calls the domain method and saves it. Repository writes join the
//! active Unit of Work, so a caller inside `UnitOfWork::run` changes the
//! courier in the same local transaction as its own aggregates (FR-310,
//! FR-485).

use std::error::Error;
use std::fmt::{Display, Formatter};

use crate::pkg::persistence::RepositoryError;
use crate::slices::courier::domains::courier::{
    Courier, CourierError, CourierId, CourierRepository, CourierStatus, MarketId,
};

/// Error of the courier busyness port; causes are the ones the aggregate and
/// the repository report.
#[derive(Clone, Debug, Eq, PartialEq)]
pub enum CourierAssignmentError {
    /// No courier is registered under the id.
    CourierNotFound(CourierId),
    /// The current courier state forbids the operation.
    Precondition(CourierError),
    Repository(RepositoryError),
}

impl Display for CourierAssignmentError {
    fn fmt(&self, formatter: &mut Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::CourierNotFound(id) => write!(formatter, "courier {} not found", id.as_str()),
            Self::Precondition(error) => Display::fmt(error, formatter),
            Self::Repository(error) => Display::fmt(error, formatter),
        }
    }
}

impl Error for CourierAssignmentError {}

impl From<RepositoryError> for CourierAssignmentError {
    fn from(error: RepositoryError) -> Self {
        Self::Repository(error)
    }
}

impl From<CourierError> for CourierAssignmentError {
    fn from(error: CourierError) -> Self {
        Self::Precondition(error)
    }
}

/// Public contract of the `courier` slice for Courier Assignments.
pub trait CourierAssignment {
    /// Reads the status of a registered courier without changing it.
    fn status(&self, courier_id: &CourierId) -> Result<CourierStatus, CourierAssignmentError>;

    /// Checks without changing state that the courier works in `market_id`
    /// (the Market of the Delivery, ADR 41) and may receive a new Delivery
    /// Offer or Assignment.
    fn ensure_available(
        &self,
        courier_id: &CourierId,
        market_id: &MarketId,
    ) -> Result<(), CourierAssignmentError>;

    /// Makes an available courier of `market_id` busy with a Courier
    /// Assignment of a Delivery of that Market.
    fn occupy(
        &self,
        courier_id: &CourierId,
        market_id: &MarketId,
    ) -> Result<(), CourierAssignmentError>;

    /// Returns a busy courier to the pool of new assignments.
    fn release(&self, courier_id: &CourierId) -> Result<(), CourierAssignmentError>;
}

/// Every courier repository provides the port: the change is saved through
/// the same repository, inside whatever Unit of Work is active.
impl<R: CourierRepository + ?Sized> CourierAssignment for R {
    fn status(&self, courier_id: &CourierId) -> Result<CourierStatus, CourierAssignmentError> {
        Ok(load(self, courier_id)?.status())
    }

    fn ensure_available(
        &self,
        courier_id: &CourierId,
        market_id: &MarketId,
    ) -> Result<(), CourierAssignmentError> {
        let courier = load(self, courier_id)?;
        courier.ensure_serves(market_id)?;
        Ok(courier.ensure_available()?)
    }

    fn occupy(
        &self,
        courier_id: &CourierId,
        market_id: &MarketId,
    ) -> Result<(), CourierAssignmentError> {
        let mut courier = load(self, courier_id)?;
        courier.ensure_serves(market_id)?;
        courier.mark_busy()?;
        Ok(self.save(&courier)?)
    }

    fn release(&self, courier_id: &CourierId) -> Result<(), CourierAssignmentError> {
        let mut courier = load(self, courier_id)?;
        courier.release()?;
        Ok(self.save(&courier)?)
    }
}

fn load<R: CourierRepository + ?Sized>(
    couriers: &R,
    courier_id: &CourierId,
) -> Result<Courier, CourierAssignmentError> {
    couriers
        .get(courier_id)?
        .ok_or_else(|| CourierAssignmentError::CourierNotFound(courier_id.clone()))
}

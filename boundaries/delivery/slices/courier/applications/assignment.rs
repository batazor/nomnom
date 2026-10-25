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
    Courier, CourierError, CourierId, CourierRepository,
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
    /// Checks that the courier is registered without reading its state.
    fn ensure_registered(&self, courier_id: &CourierId) -> Result<(), CourierAssignmentError>;

    /// Checks that the courier may receive a new Delivery Offer or Assignment
    /// without changing its state.
    fn ensure_available(&self, courier_id: &CourierId) -> Result<(), CourierAssignmentError>;

    /// Makes an available courier busy with a Courier Assignment.
    fn occupy(&self, courier_id: &CourierId) -> Result<(), CourierAssignmentError>;

    /// Returns a busy courier to the pool of new assignments.
    fn release(&self, courier_id: &CourierId) -> Result<(), CourierAssignmentError>;
}

/// Every courier repository provides the port: the change is saved through
/// the same repository, inside whatever Unit of Work is active.
impl<R: CourierRepository + ?Sized> CourierAssignment for R {
    fn ensure_registered(&self, courier_id: &CourierId) -> Result<(), CourierAssignmentError> {
        load(self, courier_id).map(drop)
    }

    fn ensure_available(&self, courier_id: &CourierId) -> Result<(), CourierAssignmentError> {
        Ok(load(self, courier_id)?.ensure_available()?)
    }

    fn occupy(&self, courier_id: &CourierId) -> Result<(), CourierAssignmentError> {
        let mut courier = load(self, courier_id)?;
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

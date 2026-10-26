use std::error::Error;
use std::fmt::{Display, Formatter};

use crate::slices::courier::domains::courier::CourierId;

/// Out-of-state snapshot of a courier for assignment decisions. Geometry,
/// zones and the way distances are computed belong to the adapter.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct CourierCandidate {
    pub courier_id: CourierId,
    pub within_delivery_zone: bool,
    pub distance_to_pickup_meters: u32,
    pub active_assignments: u32,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub enum LocatorError {
    Unavailable(String),
}

impl Display for LocatorError {
    fn fmt(&self, formatter: &mut Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::Unavailable(reason) => write!(formatter, "courier locator unavailable: {reason}"),
        }
    }
}

impl Error for LocatorError {}

/// Supplies courier candidates and snapshots for assignment decisions.
pub trait CourierLocator {
    /// Couriers worth considering for a delivery; state is not filtered here.
    fn candidates(&self) -> Result<Vec<CourierCandidate>, LocatorError>;

    /// Snapshot of one courier, or `None` when its position is unknown.
    fn snapshot(&self, courier_id: &CourierId) -> Result<Option<CourierCandidate>, LocatorError>;
}

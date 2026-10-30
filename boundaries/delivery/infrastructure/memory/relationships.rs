use std::sync::{Mutex, PoisonError};

use crate::pkg::authz::RelationshipWriteError;
use crate::pkg::persistence::AuthorizationRevision;
use crate::slices::courier::applications::relationships::CourierRelationshipWriter;
use crate::slices::courier::domains::courier::CourierId;
use crate::slices::delivery::applications::relationships::DeliveryRelationshipWriter;
use crate::slices::delivery::domains::delivery::DeliveryId;

/// One relationship write requested by a use case.
#[derive(Clone, Debug, Eq, PartialEq)]
pub enum RelationshipWrite {
    /// `TOUCH courier:<id>#self@user:<id>`.
    RegisterCourier(CourierId),
    /// `TOUCH delivery:<id>#courier@user:<courier_id>`.
    AssignCourier(DeliveryId, CourierId),
    /// `DELETE delivery:<id>#courier@user:<courier_id>`.
    UnassignCourier(DeliveryId, CourierId),
}

/// Test and local relationship writer: records every successful write and can
/// simulate an authorization graph outage. It writes nowhere, so it is not a
/// production implementation. The revision of the n-th successful write is
/// `rev-<n>` ([`RecordingRelationships::revision`]).
#[derive(Debug, Default)]
pub struct RecordingRelationships {
    writes: Mutex<Vec<RelationshipWrite>>,
    failure: Mutex<Option<String>>,
}

impl RecordingRelationships {
    #[must_use]
    pub fn new() -> Self {
        Self::default()
    }

    /// Every write fails with `reason` until [`Self::set_failure`] clears it.
    #[must_use]
    pub fn failing(reason: &str) -> Self {
        let writer = Self::new();
        writer.set_failure(Some(reason));
        writer
    }

    pub fn set_failure(&self, reason: Option<&str>) {
        *self.failure.lock().unwrap_or_else(PoisonError::into_inner) = reason.map(str::to_owned);
    }

    /// Successful writes in call order.
    #[must_use]
    pub fn writes(&self) -> Vec<RelationshipWrite> {
        self.writes
            .lock()
            .unwrap_or_else(PoisonError::into_inner)
            .clone()
    }

    /// Revision returned for the `number`-th (1-based) successful write.
    #[must_use]
    pub fn revision(number: usize) -> AuthorizationRevision {
        AuthorizationRevision::new(format!("rev-{number}"))
    }

    fn record(
        &self,
        write: RelationshipWrite,
    ) -> Result<AuthorizationRevision, RelationshipWriteError> {
        if let Some(reason) = self
            .failure
            .lock()
            .unwrap_or_else(PoisonError::into_inner)
            .clone()
        {
            return Err(RelationshipWriteError(reason));
        }
        let mut writes = self.writes.lock().unwrap_or_else(PoisonError::into_inner);
        writes.push(write);
        Ok(Self::revision(writes.len()))
    }
}

impl CourierRelationshipWriter for RecordingRelationships {
    fn register_courier(
        &self,
        courier_id: &CourierId,
    ) -> Result<AuthorizationRevision, RelationshipWriteError> {
        self.record(RelationshipWrite::RegisterCourier(courier_id.clone()))
    }
}

impl DeliveryRelationshipWriter for RecordingRelationships {
    fn assign_courier(
        &self,
        delivery_id: &DeliveryId,
        courier_id: &CourierId,
    ) -> Result<AuthorizationRevision, RelationshipWriteError> {
        self.record(RelationshipWrite::AssignCourier(
            delivery_id.clone(),
            courier_id.clone(),
        ))
    }

    fn unassign_courier(
        &self,
        delivery_id: &DeliveryId,
        courier_id: &CourierId,
    ) -> Result<AuthorizationRevision, RelationshipWriteError> {
        self.record(RelationshipWrite::UnassignCourier(
            delivery_id.clone(),
            courier_id.clone(),
        ))
    }
}

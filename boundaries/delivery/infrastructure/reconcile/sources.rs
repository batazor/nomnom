//! Business side of reconciliation: the store port and the relation sources
//! `courier#self` and `delivery#courier`.

use std::collections::HashMap;
use std::sync::Arc;

use crate::infrastructure::memory::MemoryStore;
use crate::pkg::persistence::{AuthorizationRevision, RepositoryError};
#[cfg(doc)]
use crate::slices::delivery::domains::delivery::Delivery;

/// Business state read and revision writes of reconciliation. Reads see only
/// committed state; revision writes are single-statement transactions that
/// bump the aggregate version.
pub trait ReconciliationStore: Send + Sync {
    /// `(courier_id, version)` of registered couriers after `after`, ordered
    /// by id.
    fn courier_page(
        &self,
        after: &str,
        limit: usize,
    ) -> Result<Vec<(String, u64)>, RepositoryError>;

    /// Which of `ids` are registered couriers.
    fn registered_couriers(&self, ids: &[String]) -> Result<Vec<String>, RepositoryError>;

    /// `(delivery_id, courier_id, version)` of active assignments
    /// ([`Delivery::active_courier`]) after `after`, ordered by delivery id.
    /// A delivered or failed delivery keeps `courier_id` but is not listed.
    fn assignment_page(
        &self,
        after: &str,
        limit: usize,
    ) -> Result<Vec<(String, String, u64)>, RepositoryError>;

    /// `(delivery_id, courier_id)` for those of `ids` with an active
    /// assignment ([`Delivery::active_courier`]).
    fn assigned_couriers(&self, ids: &[String]) -> Result<Vec<(String, String)>, RepositoryError>;

    /// Stores the revision on a registered courier; `false` — not registered.
    fn record_courier_revision(
        &self,
        courier_id: &str,
        revision: AuthorizationRevision,
    ) -> Result<bool, RepositoryError>;

    /// Stores the revision on a delivery whose active assignment is still
    /// `courier_id`.
    fn record_assignment_revision(
        &self,
        delivery_id: &str,
        courier_id: &str,
        revision: AuthorizationRevision,
    ) -> Result<bool, RepositoryError>;
}

impl ReconciliationStore for MemoryStore {
    fn courier_page(
        &self,
        after: &str,
        limit: usize,
    ) -> Result<Vec<(String, u64)>, RepositoryError> {
        MemoryStore::courier_page(self, after, limit)
    }

    fn registered_couriers(&self, ids: &[String]) -> Result<Vec<String>, RepositoryError> {
        MemoryStore::registered_couriers(self, ids)
    }

    fn assignment_page(
        &self,
        after: &str,
        limit: usize,
    ) -> Result<Vec<(String, String, u64)>, RepositoryError> {
        MemoryStore::assignment_page(self, after, limit)
    }

    fn assigned_couriers(&self, ids: &[String]) -> Result<Vec<(String, String)>, RepositoryError> {
        MemoryStore::assigned_couriers(self, ids)
    }

    fn record_courier_revision(
        &self,
        courier_id: &str,
        revision: AuthorizationRevision,
    ) -> Result<bool, RepositoryError> {
        MemoryStore::record_courier_revision(self, courier_id, revision)
    }

    fn record_assignment_revision(
        &self,
        delivery_id: &str,
        courier_id: &str,
        revision: AuthorizationRevision,
    ) -> Result<bool, RepositoryError> {
        MemoryStore::record_assignment_revision(self, delivery_id, courier_id, revision)
    }
}

/// `(resource_id, subject_id, version)`: a business row and the version of
/// the Courier or Delivery row it was read from. Every committed write of the
/// row bumps the version, so an unchanged version on two passes means no
/// writer committed in between (FR-563).
pub type SourceRow = (String, String, u64);

/// Business side of one relation: `resource_id → subject_id`.
pub trait RelationSource: Send + Sync {
    /// Subjects that business state assigns to those of `resource_ids` it knows.
    fn subjects_of(&self, resource_ids: &[String]) -> Result<HashMap<String, String>, String>;

    /// `(resource_id, subject_id)` after `after`, ordered by resource id.
    fn page(&self, after: &str, limit: usize) -> Result<Vec<SourceRow>, String>;

    /// Stores the revision of a restored relationship with the resource, if
    /// business state still assigns `subject_id` to it.
    fn record_revision(
        &self,
        resource_id: &str,
        subject_id: &str,
        revision: AuthorizationRevision,
    ) -> Result<(), String>;
}

/// `courier#self`: every registered courier acts for itself.
pub struct RegisteredCouriers(pub Arc<dyn ReconciliationStore>);

impl RelationSource for RegisteredCouriers {
    fn subjects_of(&self, resource_ids: &[String]) -> Result<HashMap<String, String>, String> {
        let registered = self
            .0
            .registered_couriers(resource_ids)
            .map_err(|error| error.to_string())?;
        Ok(registered.into_iter().map(|id| (id.clone(), id)).collect())
    }

    fn page(&self, after: &str, limit: usize) -> Result<Vec<SourceRow>, String> {
        let page = self
            .0
            .courier_page(after, limit)
            .map_err(|error| error.to_string())?;
        Ok(page
            .into_iter()
            .map(|(id, version)| (id.clone(), id, version))
            .collect())
    }

    fn record_revision(
        &self,
        resource_id: &str,
        _subject_id: &str,
        revision: AuthorizationRevision,
    ) -> Result<(), String> {
        self.0
            .record_courier_revision(resource_id, revision)
            .map(drop)
            .map_err(|error| error.to_string())
    }
}

/// `delivery#courier`: active assignments (FR-444, FR-563). The relationship
/// of a delivered or failed delivery is stale and removed after grace; a
/// missing one is never restored (AC-693).
pub struct AssignedDeliveries(pub Arc<dyn ReconciliationStore>);

impl RelationSource for AssignedDeliveries {
    fn subjects_of(&self, resource_ids: &[String]) -> Result<HashMap<String, String>, String> {
        Ok(self
            .0
            .assigned_couriers(resource_ids)
            .map_err(|error| error.to_string())?
            .into_iter()
            .collect())
    }

    fn page(&self, after: &str, limit: usize) -> Result<Vec<SourceRow>, String> {
        self.0
            .assignment_page(after, limit)
            .map_err(|error| error.to_string())
    }

    fn record_revision(
        &self,
        resource_id: &str,
        subject_id: &str,
        revision: AuthorizationRevision,
    ) -> Result<(), String> {
        self.0
            .record_assignment_revision(resource_id, subject_id, revision)
            .map(drop)
            .map_err(|error| error.to_string())
    }
}

use std::sync::PoisonError;

use crate::pkg::persistence::{AuthorizationRevision, RepositoryError};
use crate::slices::courier::domains::courier::CourierId;

use super::{MemoryStore, StoreState};

/// Business side of relationship reconciliation. Reads wait for the running
/// Unit of Work and see only committed state, so a relationship deleted
/// before commit is not restored from uncommitted data. Must not be called
/// inside a Unit of Work (the transaction lock is not reentrant).
impl MemoryStore {
    fn committed<T>(&self, read: impl FnOnce(&StoreState) -> T) -> Result<T, RepositoryError> {
        let _transaction = self
            .transaction
            .lock()
            .unwrap_or_else(PoisonError::into_inner);
        let state = self.available()?;
        Ok(read(&state))
    }

    /// Registered courier ids after `after`, ordered by id.
    pub fn courier_page(&self, after: &str, limit: usize) -> Result<Vec<String>, RepositoryError> {
        self.committed(|state| {
            state
                .couriers
                .keys()
                .filter(|id| id.as_str() > after)
                .take(limit)
                .cloned()
                .collect()
        })
    }

    /// Which of `ids` are registered couriers.
    pub fn registered_couriers(&self, ids: &[String]) -> Result<Vec<String>, RepositoryError> {
        self.committed(|state| {
            ids.iter()
                .filter(|id| state.couriers.contains_key(id.as_str()))
                .cloned()
                .collect()
        })
    }

    /// `(delivery_id, courier_id)` of deliveries with an assigned courier
    /// after `after`, ordered by delivery id.
    pub fn assignment_page(
        &self,
        after: &str,
        limit: usize,
    ) -> Result<Vec<(String, String)>, RepositoryError> {
        self.committed(|state| {
            state
                .deliveries
                .range::<str, _>((std::ops::Bound::Excluded(after), std::ops::Bound::Unbounded))
                .filter_map(|(id, record)| {
                    record
                        .courier_id
                        .as_ref()
                        .map(|courier| (id.clone(), courier.as_str().to_owned()))
                })
                .take(limit)
                .collect()
        })
    }

    /// Stores the revision of a relationship restored by reconciliation on a
    /// registered courier. `false` — the courier is not registered.
    pub fn record_courier_revision(
        &self,
        courier_id: &str,
        revision: AuthorizationRevision,
    ) -> Result<bool, RepositoryError> {
        let _transaction = self
            .transaction
            .lock()
            .unwrap_or_else(PoisonError::into_inner);
        let mut state = self.available()?;
        let Some(courier) = state.couriers.get_mut(courier_id) else {
            return Ok(false);
        };
        courier.record_authorization_revision(revision);
        *courier = courier.clone().with_version(courier.version() + 1);
        Ok(true)
    }

    /// Stores the revision of a restored `delivery#courier` relationship when
    /// the delivery is still assigned to `courier_id`.
    pub fn record_assignment_revision(
        &self,
        delivery_id: &str,
        courier_id: &str,
        revision: AuthorizationRevision,
    ) -> Result<bool, RepositoryError> {
        let _transaction = self
            .transaction
            .lock()
            .unwrap_or_else(PoisonError::into_inner);
        let mut state = self.available()?;
        let Some(record) = state.deliveries.get_mut(delivery_id) else {
            return Ok(false);
        };
        if record.courier_id.as_ref().map(CourierId::as_str) != Some(courier_id) {
            return Ok(false);
        }
        record.authorization_revision = Some(revision);
        record.version += 1;
        Ok(true)
    }

    /// `(delivery_id, courier_id)` for those of `ids` with an assigned courier.
    pub fn assigned_couriers(
        &self,
        ids: &[String],
    ) -> Result<Vec<(String, String)>, RepositoryError> {
        self.committed(|state| {
            ids.iter()
                .filter_map(|id| {
                    let courier = state.deliveries.get(id.as_str())?.courier_id.as_ref()?;
                    Some((id.clone(), courier.as_str().to_owned()))
                })
                .collect()
        })
    }
}

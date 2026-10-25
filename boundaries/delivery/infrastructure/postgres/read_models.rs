//! Query read models and the reconciliation reads of [`PostgresStore`].

use crate::infrastructure::reconcile::ReconciliationStore;
use crate::pkg::persistence::{AuthorizationRevision, RepositoryError};
use crate::slices::courier::domains::courier::CourierId;
use crate::slices::delivery::applications::get_courier_current_delivery::{
    CourierCurrentDeliveryReadModel, CurrentDeliveryView,
};
use crate::slices::delivery::applications::list_available_delivery_offers::{
    AvailableDeliveryOfferView, PendingDeliveryOffersReadModel,
};
use crate::slices::delivery::domains::delivery::{DeliveryId, DeliveryOfferId, OrderId};

use super::rows::{address, column, parse_delivery_state};
use super::{PostgresStore, limit, storage_error};

impl CourierCurrentDeliveryReadModel for PostgresStore {
    fn active_delivery_for(
        &self,
        courier_id: &CourierId,
    ) -> Result<Option<CurrentDeliveryView>, RepositoryError> {
        sql!(self, |connection| sqlx::query(
            "SELECT id, order_id, pickup_address, dropoff_address, state FROM deliveries
             WHERE courier_id = $1 AND state IN ('assigned', 'picked_up')
             ORDER BY id LIMIT 1"
        )
        .bind(courier_id.as_str())
        .fetch_optional(connection))?
        .map(|row| {
            Ok(CurrentDeliveryView {
                delivery_id: DeliveryId::new(column::<String>(&row, "id")?),
                order_id: OrderId::new(column::<String>(&row, "order_id")?),
                pickup_address: address(column(&row, "pickup_address")?)?,
                dropoff_address: address(column(&row, "dropoff_address")?)?,
                state: parse_delivery_state(column(&row, "state")?)?,
            })
        })
        .transpose()
    }
}

impl PendingDeliveryOffersReadModel for PostgresStore {
    fn pending_offers_for(
        &self,
        courier_id: &CourierId,
    ) -> Result<Vec<AvailableDeliveryOfferView>, RepositoryError> {
        sql!(self, |connection| sqlx::query(
            "SELECT o.id, o.delivery_id, d.pickup_address, d.dropoff_address, o.expires_at
             FROM delivery_offers o JOIN deliveries d ON d.id = o.delivery_id
             WHERE o.courier_id = $1 AND o.state = 'pending'
             ORDER BY o.position"
        )
        .bind(courier_id.as_str())
        .fetch_all(connection))?
        .iter()
        .map(|row| {
            Ok(AvailableDeliveryOfferView {
                offer_id: DeliveryOfferId::new(column::<String>(row, "id")?),
                delivery_id: DeliveryId::new(column::<String>(row, "delivery_id")?),
                pickup_address: address(column(row, "pickup_address")?)?,
                dropoff_address: address(column(row, "dropoff_address")?)?,
                expires_at_epoch_seconds: column(row, "expires_at")?,
            })
        })
        .collect()
    }
}

impl ReconciliationStore for PostgresStore {
    fn courier_page(&self, after: &str, size: usize) -> Result<Vec<String>, RepositoryError> {
        sql!(self, |connection| sqlx::query_scalar(
            "SELECT id FROM couriers WHERE id > $1 ORDER BY id LIMIT $2"
        )
        .bind(after)
        .bind(limit(size))
        .fetch_all(connection))
    }

    fn registered_couriers(&self, ids: &[String]) -> Result<Vec<String>, RepositoryError> {
        sql!(self, |connection| sqlx::query_scalar(
            "SELECT id FROM couriers WHERE id = ANY($1)"
        )
        .bind(ids)
        .fetch_all(connection))
    }

    fn assignment_page(
        &self,
        after: &str,
        size: usize,
    ) -> Result<Vec<(String, String)>, RepositoryError> {
        sql!(self, |connection| sqlx::query_as(
            "SELECT id, courier_id FROM deliveries
             WHERE id > $1 AND courier_id IS NOT NULL ORDER BY id LIMIT $2"
        )
        .bind(after)
        .bind(limit(size))
        .fetch_all(connection))
    }

    fn assigned_couriers(&self, ids: &[String]) -> Result<Vec<(String, String)>, RepositoryError> {
        sql!(self, |connection| sqlx::query_as(
            "SELECT id, courier_id FROM deliveries
             WHERE id = ANY($1) AND courier_id IS NOT NULL"
        )
        .bind(ids)
        .fetch_all(connection))
    }

    fn record_courier_revision(
        &self,
        courier_id: &str,
        revision: AuthorizationRevision,
    ) -> Result<bool, RepositoryError> {
        let updated = sql!(self, |connection| sqlx::query(
            "UPDATE couriers SET authorization_revision = $2, version = version + 1
             WHERE id = $1"
        )
        .bind(courier_id)
        .bind(revision.as_str())
        .execute(connection))?;
        Ok(updated.rows_affected() > 0)
    }

    fn record_assignment_revision(
        &self,
        delivery_id: &str,
        courier_id: &str,
        revision: AuthorizationRevision,
    ) -> Result<bool, RepositoryError> {
        let updated = sql!(self, |connection| sqlx::query(
            "UPDATE deliveries SET authorization_revision = $3, version = version + 1
             WHERE id = $1 AND courier_id = $2"
        )
        .bind(delivery_id)
        .bind(courier_id)
        .bind(revision.as_str())
        .execute(connection))?;
        Ok(updated.rows_affected() > 0)
    }
}

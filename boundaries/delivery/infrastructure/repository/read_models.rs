//! Query read models, the courier positions of the locator and the
//! reconciliation reads of [`Store`].

use crate::infrastructure::locator::CourierPositions;
use crate::infrastructure::reconcile::ReconciliationStore;
use crate::pkg::persistence::{AuthorizationRevision, RepositoryError};
use crate::slices::courier::applications::update_courier_location::CourierPosition;
use crate::slices::courier::domains::courier::{CourierId, CourierStatus, Location};
use crate::slices::delivery::applications::dispatch_deliveries::{
    DeliveryPosition, WaitingDeliveries,
};
use crate::slices::delivery::applications::expire_delivery_offers::ExpiredDeliveryOffers;
use crate::slices::delivery::applications::get_courier_current_delivery::{
    CourierCurrentDeliveryReadModel, CurrentDeliveryView,
};
use crate::slices::delivery::applications::list_available_delivery_offers::{
    AvailableDeliveryOfferView, PendingDeliveryOffersReadModel,
};
use crate::slices::delivery::applications::offers_of_delivery::OffersOfDelivery;
use crate::slices::delivery::domains::delivery::{
    Delivery, DeliveryId, DeliveryOfferId, OfferState, OrderId, State,
};

use super::rows::{
    DeliveryRow, active_assignment_states, courier_status, delivery_from_row, delivery_state,
    offer_state, parse_delivery_state,
};
use super::{Store, corrupt, domain_version, limit, storage_error};

impl CourierCurrentDeliveryReadModel for Store {
    fn active_delivery_for(
        &self,
        courier_id: &CourierId,
    ) -> Result<Option<CurrentDeliveryView>, RepositoryError> {
        sql!(self, "SELECT", |connection| sqlx::query!(
            "SELECT id, order_id, state FROM deliveries
             WHERE courier_id = $1 AND state = ANY($2)
             ORDER BY id LIMIT 1",
            courier_id.as_str(),
            &active_assignment_states()[..] as &[&str]
        )
        .fetch_optional(connection))?
        .map(|row| {
            Ok(CurrentDeliveryView {
                delivery_id: DeliveryId::new(row.id),
                order_id: OrderId::new(row.order_id),
                state: parse_delivery_state(&row.state)?,
            })
        })
        .transpose()
    }
}

impl WaitingDeliveries for Store {
    fn list_waiting_without_pending_offer(
        &self,
        after: Option<&DeliveryPosition>,
        limit: usize,
    ) -> Result<Vec<Delivery>, RepositoryError> {
        // Without a cursor every row is after (i64::MIN, ''): ids are "C"
        // collated, so the empty string sorts first.
        let (created_at, id) = after.map_or((i64::MIN, ""), DeliveryPosition::key);
        sql!(self, "SELECT", |connection| sqlx::query_as!(
            DeliveryRow,
            "SELECT id, order_id, courier_id, state, failure_reason,
                 failure_detail, authorization_revision, version, created_at,
                 expected_pickup_at, expected_delivered_at
             FROM deliveries
             WHERE state = $1 AND (COALESCE(created_at, 0), id) > ($2, $3)
               AND NOT EXISTS (SELECT 1 FROM delivery_offers o
                               WHERE o.delivery_id = deliveries.id AND o.state = $4)
             ORDER BY COALESCE(created_at, 0), id LIMIT $5",
            delivery_state(State::Waiting),
            created_at,
            id,
            offer_state(OfferState::Pending),
            super::limit(limit)
        )
        .fetch_all(connection))?
        .into_iter()
        .map(delivery_from_row)
        .collect()
    }
}

impl OffersOfDelivery for Store {
    fn offer_ids_of(
        &self,
        delivery_id: &DeliveryId,
    ) -> Result<Vec<DeliveryOfferId>, RepositoryError> {
        sql!(self, "SELECT", |connection| {
            sqlx::query_scalar!(
                "SELECT id FROM delivery_offers WHERE delivery_id = $1 ORDER BY position",
                delivery_id.as_str()
            )
            .fetch_all(connection)
        })
        .map(|ids| ids.into_iter().map(DeliveryOfferId::new).collect())
    }
}

impl ExpiredDeliveryOffers for Store {
    fn expired_pending_offer_ids(
        &self,
        now_epoch_seconds: i64,
        limit: usize,
    ) -> Result<Vec<DeliveryOfferId>, RepositoryError> {
        sql!(self, "SELECT", |connection| {
            sqlx::query_scalar!(
                "SELECT id FROM delivery_offers
                 WHERE state = $1 AND expires_at < $2
                 ORDER BY expires_at, position LIMIT $3",
                offer_state(OfferState::Pending),
                now_epoch_seconds,
                super::limit(limit)
            )
            .fetch_all(connection)
        })
        .map(|ids| ids.into_iter().map(DeliveryOfferId::new).collect())
    }
}

impl PendingDeliveryOffersReadModel for Store {
    fn pending_offers_for(
        &self,
        courier_id: &CourierId,
    ) -> Result<Vec<AvailableDeliveryOfferView>, RepositoryError> {
        Ok(sql!(self, "SELECT", |connection| sqlx::query!(
            "SELECT o.id, o.delivery_id, d.order_id, o.expires_at
             FROM delivery_offers o JOIN deliveries d ON d.id = o.delivery_id
             WHERE o.courier_id = $1 AND o.state = 'pending'
             ORDER BY o.position",
            courier_id.as_str()
        )
        .fetch_all(connection))?
        .into_iter()
        .map(|row| AvailableDeliveryOfferView {
            offer_id: DeliveryOfferId::new(row.id),
            delivery_id: DeliveryId::new(row.delivery_id),
            order_id: OrderId::new(row.order_id),
            expires_at_epoch_seconds: row.expires_at,
        })
        .collect())
    }
}

impl CourierPositions for Store {
    fn available_positions(&self) -> Result<Vec<CourierPosition>, RepositoryError> {
        sql!(self, "SELECT", |connection| sqlx::query!(
            "SELECT p.courier_id, p.latitude, p.longitude, p.recorded_at
             FROM courier_positions p JOIN couriers c ON c.id = p.courier_id
             WHERE c.status = $1
             ORDER BY p.courier_id",
            courier_status(CourierStatus::Available)
        )
        .fetch_all(connection))?
        .into_iter()
        .map(|row| {
            Ok(CourierPosition {
                courier_id: CourierId::new(row.courier_id),
                location: Location::new(row.latitude, row.longitude)
                    .map_err(|error| corrupt("courier position", error))?,
                recorded_at_epoch_seconds: row.recorded_at,
            })
        })
        .collect()
    }
}

impl ReconciliationStore for Store {
    fn courier_page(
        &self,
        after: &str,
        size: usize,
    ) -> Result<Vec<(String, u64)>, RepositoryError> {
        sql!(self, "SELECT", |connection| sqlx::query!(
            "SELECT id, version FROM couriers WHERE id > $1 ORDER BY id LIMIT $2",
            after,
            limit(size)
        )
        .fetch_all(connection))?
        .into_iter()
        .map(|row| Ok((row.id, domain_version(row.version)?)))
        .collect()
    }

    fn registered_couriers(&self, ids: &[String]) -> Result<Vec<String>, RepositoryError> {
        sql!(self, "SELECT", |connection| sqlx::query_scalar!(
            "SELECT id FROM couriers WHERE id = ANY($1)",
            ids
        )
        .fetch_all(connection))
    }

    fn assignment_page(
        &self,
        after: &str,
        size: usize,
    ) -> Result<Vec<(String, String, u64)>, RepositoryError> {
        // `courier_id IS NOT NULL` narrows the nullable column: `courier_id!`.
        sql!(self, "SELECT", |connection| sqlx::query!(
            r#"SELECT id, courier_id AS "courier_id!", version FROM deliveries
             WHERE id > $1 AND courier_id IS NOT NULL AND state = ANY($3)
             ORDER BY id LIMIT $2"#,
            after,
            limit(size),
            &active_assignment_states()[..] as &[&str]
        )
        .fetch_all(connection))?
        .into_iter()
        .map(|row| Ok((row.id, row.courier_id, domain_version(row.version)?)))
        .collect()
    }

    fn assigned_couriers(&self, ids: &[String]) -> Result<Vec<(String, String)>, RepositoryError> {
        sql!(self, "SELECT", |connection| sqlx::query!(
            r#"SELECT id, courier_id AS "courier_id!" FROM deliveries
             WHERE id = ANY($1) AND courier_id IS NOT NULL AND state = ANY($2)"#,
            ids,
            &active_assignment_states()[..] as &[&str]
        )
        .fetch_all(connection))
        .map(|rows| {
            rows.into_iter()
                .map(|row| (row.id, row.courier_id))
                .collect()
        })
    }

    fn record_courier_revision(
        &self,
        courier_id: &str,
        revision: AuthorizationRevision,
    ) -> Result<bool, RepositoryError> {
        let updated = sql!(self, "UPDATE", |connection| sqlx::query!(
            "UPDATE couriers SET authorization_revision = $2, version = version + 1
             WHERE id = $1",
            courier_id,
            revision.as_str()
        )
        .execute(connection))?;
        Ok(updated.rows_affected() > 0)
    }

    fn record_assignment_revision(
        &self,
        delivery_id: &str,
        courier_id: &str,
        revision: AuthorizationRevision,
    ) -> Result<bool, RepositoryError> {
        let updated = sql!(self, "UPDATE", |connection| sqlx::query!(
            "UPDATE deliveries SET authorization_revision = $3, version = version + 1
             WHERE id = $1 AND courier_id = $2 AND state = ANY($4)",
            delivery_id,
            courier_id,
            revision.as_str(),
            &active_assignment_states()[..] as &[&str]
        )
        .execute(connection))?;
        Ok(updated.rows_affected() > 0)
    }
}

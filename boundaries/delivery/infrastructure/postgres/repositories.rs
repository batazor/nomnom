//! Repositories and the courier location store of [`PostgresStore`] with
//! optimistic concurrency by `version`.

use crate::pkg::persistence::{AuthorizationRevision, RepositoryError};
use crate::slices::courier::applications::update_courier_location::{
    CourierLocationStore, CourierPosition,
};
use crate::slices::courier::domains::courier::{Courier, CourierId, CourierRepository, Location};
use crate::slices::delivery::domains::delivery::{
    Delivery, DeliveryFailureReason, DeliveryId, DeliveryOffer, DeliveryOfferId,
    DeliveryOfferRepository, DeliveryPosition, DeliveryRepository, OfferState, OrderId, State,
};

use super::rows::{
    COURIER_COLUMNS, DELIVERY_COLUMNS, OFFER_COLUMNS, column, courier_from_row, courier_status,
    delivery_from_row, delivery_state, offer_from_row, offer_state,
};
use super::{PostgresStore, corrupt, db_version, storage_error};

impl PostgresStore {
    /// Maps zero updated rows: a missing id is a [`RepositoryError::Conflict`]
    /// (port contract), an existing id a lost optimistic concurrency race.
    fn optimistic(
        &self,
        updated: u64,
        exists_sql: &'static str,
        id: &str,
    ) -> Result<(), RepositoryError> {
        if updated > 0 {
            return Ok(());
        }
        let exists = sql!(self, |connection| sqlx::query(exists_sql)
            .bind(id)
            .fetch_optional(connection))?
        .is_some();
        Err(if exists {
            RepositoryError::ConcurrentModification
        } else {
            RepositoryError::Conflict
        })
    }
}

impl CourierRepository for PostgresStore {
    fn get(&self, id: &CourierId) -> Result<Option<Courier>, RepositoryError> {
        let statement = format!("SELECT {COURIER_COLUMNS} FROM couriers WHERE id = $1");
        sql!(self, |connection| sqlx::query(sqlx::AssertSqlSafe(
            statement
        ))
        .bind(id.as_str())
        .fetch_optional(connection))?
        .as_ref()
        .map(courier_from_row)
        .transpose()
    }

    fn add(&self, courier: &Courier) -> Result<(), RepositoryError> {
        let version = db_version(courier.version())?;
        let inserted = sql!(self, |connection| sqlx::query(
            "INSERT INTO couriers (id, status, authorization_revision, version)
             VALUES ($1, $2, $3, $4) ON CONFLICT DO NOTHING"
        )
        .bind(courier.id().as_str())
        .bind(courier_status(courier.status()))
        .bind(
            courier
                .authorization_revision()
                .map(AuthorizationRevision::as_str)
        )
        .bind(version)
        .execute(connection))?;
        if inserted.rows_affected() == 0 {
            return Err(RepositoryError::Conflict);
        }
        Ok(())
    }

    fn save(&self, courier: &Courier) -> Result<(), RepositoryError> {
        let version = db_version(courier.version())?;
        let updated = sql!(self, |connection| sqlx::query(
            "UPDATE couriers SET status = $3, authorization_revision = $4,
                 version = version + 1
             WHERE id = $1 AND version = $2"
        )
        .bind(courier.id().as_str())
        .bind(version)
        .bind(courier_status(courier.status()))
        .bind(
            courier
                .authorization_revision()
                .map(AuthorizationRevision::as_str)
        )
        .execute(connection))?;
        self.optimistic(
            updated.rows_affected(),
            "SELECT 1 FROM couriers WHERE id = $1",
            courier.id().as_str(),
        )
    }
}

impl DeliveryRepository for PostgresStore {
    fn get(&self, id: &DeliveryId) -> Result<Option<Delivery>, RepositoryError> {
        let statement = format!("SELECT {DELIVERY_COLUMNS} FROM deliveries WHERE id = $1");
        sql!(self, |connection| sqlx::query(sqlx::AssertSqlSafe(
            statement
        ))
        .bind(id.as_str())
        .fetch_optional(connection))?
        .as_ref()
        .map(delivery_from_row)
        .transpose()
    }

    fn find_by_order(&self, order_id: &OrderId) -> Result<Option<Delivery>, RepositoryError> {
        let statement = format!("SELECT {DELIVERY_COLUMNS} FROM deliveries WHERE order_id = $1");
        sql!(self, |connection| sqlx::query(sqlx::AssertSqlSafe(
            statement
        ))
        .bind(order_id.as_str())
        .fetch_optional(connection))?
        .as_ref()
        .map(delivery_from_row)
        .transpose()
    }

    fn add(&self, delivery: &Delivery) -> Result<(), RepositoryError> {
        let version = db_version(delivery.version())?;
        // ON CONFLICT DO NOTHING keeps the transaction usable after a
        // duplicate id or order id.
        let inserted = sql!(self, |connection| sqlx::query(
            "INSERT INTO deliveries (id, order_id, courier_id, pickup_address,
                 dropoff_address, state, failure_reason, authorization_revision, version,
                 created_at, failure_detail)
             VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11) ON CONFLICT DO NOTHING"
        )
        .bind(delivery.id().as_str())
        .bind(delivery.order_id().as_str())
        .bind(delivery.courier_id().map(CourierId::as_str))
        .bind(delivery.pickup_address().as_str())
        .bind(delivery.dropoff_address().as_str())
        .bind(delivery_state(delivery.state()))
        .bind(delivery.failure_reason().map(DeliveryFailureReason::as_str))
        .bind(
            delivery
                .authorization_revision()
                .map(AuthorizationRevision::as_str)
        )
        .bind(version)
        .bind(delivery.created_at_epoch_seconds())
        .bind(
            delivery
                .failure_reason()
                .and_then(DeliveryFailureReason::detail)
        )
        .execute(connection))?;
        if inserted.rows_affected() == 0 {
            return Err(RepositoryError::Conflict);
        }
        Ok(())
    }

    fn save(&self, delivery: &Delivery) -> Result<(), RepositoryError> {
        let version = db_version(delivery.version())?;
        let updated = sql!(self, |connection| sqlx::query(
            "UPDATE deliveries SET courier_id = $3, state = $4, failure_reason = $5,
                 authorization_revision = $6, failure_detail = $7, version = version + 1
             WHERE id = $1 AND version = $2"
        )
        .bind(delivery.id().as_str())
        .bind(version)
        .bind(delivery.courier_id().map(CourierId::as_str))
        .bind(delivery_state(delivery.state()))
        .bind(delivery.failure_reason().map(DeliveryFailureReason::as_str))
        .bind(
            delivery
                .authorization_revision()
                .map(AuthorizationRevision::as_str)
        )
        .bind(
            delivery
                .failure_reason()
                .and_then(DeliveryFailureReason::detail)
        )
        .execute(connection))?;
        self.optimistic(
            updated.rows_affected(),
            "SELECT 1 FROM deliveries WHERE id = $1",
            delivery.id().as_str(),
        )
    }

    fn list_waiting_without_pending_offer(
        &self,
        after: Option<&DeliveryPosition>,
        limit: usize,
    ) -> Result<Vec<Delivery>, RepositoryError> {
        // Without a cursor every row is after (i64::MIN, ''): ids are "C"
        // collated, so the empty string sorts first.
        let (created_at, id) = after.map_or((i64::MIN, ""), DeliveryPosition::key);
        let statement = format!(
            "SELECT {DELIVERY_COLUMNS} FROM deliveries
             WHERE state = $1 AND (COALESCE(created_at, 0), id) > ($2, $3)
               AND NOT EXISTS (SELECT 1 FROM delivery_offers o
                               WHERE o.delivery_id = deliveries.id AND o.state = $4)
             ORDER BY COALESCE(created_at, 0), id LIMIT $5"
        );
        sql!(self, |connection| sqlx::query(sqlx::AssertSqlSafe(
            statement
        ))
        .bind(delivery_state(State::Waiting))
        .bind(created_at)
        .bind(id)
        .bind(offer_state(OfferState::Pending))
        .bind(super::limit(limit))
        .fetch_all(connection))?
        .iter()
        .map(delivery_from_row)
        .collect()
    }
}

impl DeliveryOfferRepository for PostgresStore {
    fn get(&self, id: &DeliveryOfferId) -> Result<Option<DeliveryOffer>, RepositoryError> {
        let statement = format!("SELECT {OFFER_COLUMNS} FROM delivery_offers WHERE id = $1");
        sql!(self, |connection| sqlx::query(sqlx::AssertSqlSafe(
            statement
        ))
        .bind(id.as_str())
        .fetch_optional(connection))?
        .as_ref()
        .map(offer_from_row)
        .transpose()
    }

    fn list_for_delivery(
        &self,
        delivery_id: &DeliveryId,
    ) -> Result<Vec<DeliveryOffer>, RepositoryError> {
        let statement = format!(
            "SELECT {OFFER_COLUMNS} FROM delivery_offers WHERE delivery_id = $1 ORDER BY position"
        );
        sql!(self, |connection| sqlx::query(sqlx::AssertSqlSafe(
            statement
        ))
        .bind(delivery_id.as_str())
        .fetch_all(connection))?
        .iter()
        .map(offer_from_row)
        .collect()
    }

    fn list_expired_pending(
        &self,
        now_epoch_seconds: i64,
        limit: usize,
    ) -> Result<Vec<DeliveryOffer>, RepositoryError> {
        let statement = format!(
            "SELECT {OFFER_COLUMNS} FROM delivery_offers
             WHERE state = 'pending' AND expires_at < $1
             ORDER BY expires_at, position LIMIT $2"
        );
        sql!(self, |connection| sqlx::query(sqlx::AssertSqlSafe(
            statement
        ))
        .bind(now_epoch_seconds)
        .bind(super::limit(limit))
        .fetch_all(connection))?
        .iter()
        .map(offer_from_row)
        .collect()
    }

    fn add(&self, offer: &DeliveryOffer) -> Result<(), RepositoryError> {
        let version = db_version(offer.version())?;
        let inserted = sql!(self, |connection| sqlx::query(
            "INSERT INTO delivery_offers (id, delivery_id, courier_id, offered_at,
                 expires_at, state, version)
             VALUES ($1, $2, $3, $4, $5, $6, $7) ON CONFLICT DO NOTHING"
        )
        .bind(offer.id().as_str())
        .bind(offer.delivery_id().as_str())
        .bind(offer.courier_id().as_str())
        .bind(offer.offered_at_epoch_seconds())
        .bind(offer.expires_at_epoch_seconds())
        .bind(offer_state(offer.state()))
        .bind(version)
        .execute(connection))?;
        if inserted.rows_affected() == 0 {
            return Err(RepositoryError::Conflict);
        }
        Ok(())
    }

    fn save(&self, offer: &DeliveryOffer) -> Result<(), RepositoryError> {
        let version = db_version(offer.version())?;
        let updated = sql!(self, |connection| sqlx::query(
            "UPDATE delivery_offers SET state = $3, version = version + 1
             WHERE id = $1 AND version = $2"
        )
        .bind(offer.id().as_str())
        .bind(version)
        .bind(offer_state(offer.state()))
        .execute(connection))?;
        self.optimistic(
            updated.rows_affected(),
            "SELECT 1 FROM delivery_offers WHERE id = $1",
            offer.id().as_str(),
        )
    }
}

impl CourierLocationStore for PostgresStore {
    fn record(&self, position: &CourierPosition) -> Result<(), RepositoryError> {
        sql!(self, |connection| sqlx::query(
            "INSERT INTO courier_positions (courier_id, latitude, longitude, recorded_at)
             VALUES ($1, $2, $3, $4)
             ON CONFLICT (courier_id) DO UPDATE SET latitude = EXCLUDED.latitude,
                 longitude = EXCLUDED.longitude, recorded_at = EXCLUDED.recorded_at"
        )
        .bind(position.courier_id.as_str())
        .bind(position.location.latitude())
        .bind(position.location.longitude())
        .bind(position.recorded_at_epoch_seconds)
        .execute(connection))
        .map(drop)
    }

    fn latest(&self, courier_id: &CourierId) -> Result<Option<CourierPosition>, RepositoryError> {
        sql!(self, |connection| sqlx::query(
            "SELECT latitude, longitude, recorded_at FROM courier_positions
             WHERE courier_id = $1"
        )
        .bind(courier_id.as_str())
        .fetch_optional(connection))?
        .map(|row| {
            Ok(CourierPosition {
                courier_id: courier_id.clone(),
                location: Location::new(column(&row, "latitude")?, column(&row, "longitude")?)
                    .map_err(|error| corrupt("courier position", error))?,
                recorded_at_epoch_seconds: column(&row, "recorded_at")?,
            })
        })
        .transpose()
    }
}

//! Repositories and the courier location store of [`Store`] with
//! optimistic concurrency by `version`.

use crate::pkg::persistence::{AuthorizationRevision, RepositoryError};
use crate::slices::courier::applications::update_courier_location::{
    CourierLocationStore, CourierPosition,
};
use crate::slices::courier::domains::courier::{Courier, CourierId, CourierRepository, Location};
use crate::slices::delivery::domains::delivery::{
    Delivery, DeliveryFailureReason, DeliveryId, DeliveryOffer, DeliveryOfferId,
    DeliveryOfferRepository, DeliveryRepository, ExpectedTime, OrderId,
};

use super::outbox::row_traceparent;
use super::rows::{
    CourierRow, DeliveryRow, OfferRow, courier_from_row, courier_status, delivery_from_row,
    delivery_state, offer_from_row, offer_state,
};
use super::{Store, corrupt, db_version, storage_error};

/// Maps zero updated rows: a missing id is a [`RepositoryError::Conflict`]
/// (port contract), an existing id a lost optimistic concurrency race.
/// `exists` reads whether the row is there; it runs only after zero rows.
fn optimistic(
    updated: u64,
    exists: impl FnOnce() -> Result<bool, RepositoryError>,
) -> Result<(), RepositoryError> {
    if updated > 0 {
        return Ok(());
    }
    Err(if exists()? {
        RepositoryError::ConcurrentModification
    } else {
        RepositoryError::Conflict
    })
}

impl CourierRepository for Store {
    fn get(&self, id: &CourierId) -> Result<Option<Courier>, RepositoryError> {
        sql!(self, "SELECT", |connection| sqlx::query_as!(
            CourierRow,
            "SELECT id, status, authorization_revision, version FROM couriers WHERE id = $1",
            id.as_str()
        )
        .fetch_optional(connection))?
        .map(courier_from_row)
        .transpose()
    }

    // Courier writes run without a Unit of Work too (registration,
    // availability), so each write and its Change Notification (ADR 36) are
    // one statement: the CTE `changed` feeds the notification tail of
    // `outbox::notify_courier`, with the Outbox retention (`$1` is the
    // `row_traceparent`).
    fn add(&self, courier: &Courier) -> Result<(), RepositoryError> {
        let version = db_version(courier.version())?;
        let inserted = sql!(self, "INSERT", |connection| sqlx::query!(
            "WITH changed AS (
                 INSERT INTO couriers (id, status, authorization_revision, version)
                 VALUES ($2, $3, $4, $5) ON CONFLICT DO NOTHING RETURNING id
             ), expired AS (
                 DELETE FROM delivery_outbox WHERE event_id IN (
                     SELECT event_id FROM delivery_outbox
                     WHERE created_at < now() - interval '1 hour'
                     FOR UPDATE SKIP LOCKED))
             INSERT INTO delivery_outbox (event_id, event_type, aggregate_type, aggregate_id,
                 aggregate_version, topic, payload, traceparent)
             SELECT gen_random_uuid(), 'CourierChanged', 'Courier', id, 0,
                 'delivery.v1.courier-changes',
                 convert_to(json_build_object('__typename', 'Courier', 'id', id)::text, 'UTF8'),
                 $1
             FROM changed",
            row_traceparent(),
            courier.id().as_str(),
            courier_status(courier.status()),
            courier
                .authorization_revision()
                .map(AuthorizationRevision::as_str),
            version
        )
        .execute(connection))?;
        if inserted.rows_affected() == 0 {
            return Err(RepositoryError::Conflict);
        }
        Ok(())
    }

    fn save(&self, courier: &Courier) -> Result<(), RepositoryError> {
        let version = db_version(courier.version())?;
        let updated = sql!(self, "UPDATE", |connection| sqlx::query!(
            "WITH changed AS (
                 UPDATE couriers SET status = $4, authorization_revision = $5,
                     version = version + 1
                 WHERE id = $2 AND version = $3 RETURNING id
             ), expired AS (
                 DELETE FROM delivery_outbox WHERE event_id IN (
                     SELECT event_id FROM delivery_outbox
                     WHERE created_at < now() - interval '1 hour'
                     FOR UPDATE SKIP LOCKED))
             INSERT INTO delivery_outbox (event_id, event_type, aggregate_type, aggregate_id,
                 aggregate_version, topic, payload, traceparent)
             SELECT gen_random_uuid(), 'CourierChanged', 'Courier', id, 0,
                 'delivery.v1.courier-changes',
                 convert_to(json_build_object('__typename', 'Courier', 'id', id)::text, 'UTF8'),
                 $1
             FROM changed",
            row_traceparent(),
            courier.id().as_str(),
            version,
            courier_status(courier.status()),
            courier
                .authorization_revision()
                .map(AuthorizationRevision::as_str)
        )
        .execute(connection))?;
        optimistic(updated.rows_affected(), || {
            sql!(self, "SELECT", |connection| sqlx::query_scalar!(
                "SELECT 1 FROM couriers WHERE id = $1",
                courier.id().as_str()
            )
            .fetch_optional(connection))
            .map(|row| row.is_some())
        })
    }
}

impl DeliveryRepository for Store {
    fn get(&self, id: &DeliveryId) -> Result<Option<Delivery>, RepositoryError> {
        sql!(self, "SELECT", |connection| sqlx::query_as!(
            DeliveryRow,
            "SELECT id, order_id, courier_id, pickup_address, state, failure_reason,
                 failure_detail, authorization_revision, version, created_at,
                 expected_pickup_at, expected_delivered_at
             FROM deliveries WHERE id = $1",
            id.as_str()
        )
        .fetch_optional(connection))?
        .map(delivery_from_row)
        .transpose()
    }

    fn find_by_order(&self, order_id: &OrderId) -> Result<Option<Delivery>, RepositoryError> {
        sql!(self, "SELECT", |connection| sqlx::query_as!(
            DeliveryRow,
            "SELECT id, order_id, courier_id, pickup_address, state, failure_reason,
                 failure_detail, authorization_revision, version, created_at,
                 expected_pickup_at, expected_delivered_at
             FROM deliveries WHERE order_id = $1",
            order_id.as_str()
        )
        .fetch_optional(connection))?
        .map(delivery_from_row)
        .transpose()
    }

    fn add(&self, delivery: &Delivery) -> Result<(), RepositoryError> {
        let version = db_version(delivery.version())?;
        // ON CONFLICT DO NOTHING keeps the transaction usable after a
        // duplicate id or order id.
        let inserted = sql!(self, "INSERT", |connection| sqlx::query!(
            "INSERT INTO deliveries (id, order_id, courier_id, pickup_address,
                 state, failure_reason, authorization_revision, version,
                 created_at, failure_detail, expected_pickup_at, expected_delivered_at)
             VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
             ON CONFLICT DO NOTHING",
            delivery.id().as_str(),
            delivery.order_id().as_str(),
            delivery.courier_id().map(CourierId::as_str),
            delivery.pickup_address().as_str(),
            delivery_state(delivery.state()),
            delivery.failure_reason().map(DeliveryFailureReason::as_str),
            delivery
                .authorization_revision()
                .map(AuthorizationRevision::as_str),
            version,
            delivery.created_at_epoch_seconds(),
            delivery
                .failure_reason()
                .and_then(DeliveryFailureReason::detail),
            delivery
                .expected_pickup_at()
                .map(ExpectedTime::epoch_seconds),
            delivery
                .expected_delivered_at()
                .map(ExpectedTime::epoch_seconds)
        )
        .execute(connection))?;
        if inserted.rows_affected() == 0 {
            return Err(RepositoryError::Conflict);
        }
        delivery
            .courier_id()
            .map_or(Ok(()), |courier| self.notify_courier(courier.as_str()))
    }

    fn save(&self, delivery: &Delivery) -> Result<(), RepositoryError> {
        let version = db_version(delivery.version())?;
        // A released delivery leaves the courier it had: notify that one too.
        let previous: Option<Option<String>> = sql!(self, "SELECT", |connection| {
            sqlx::query_scalar!(
                "SELECT courier_id FROM deliveries WHERE id = $1",
                delivery.id().as_str()
            )
            .fetch_optional(connection)
        })?;
        let updated = sql!(self, "UPDATE", |connection| sqlx::query!(
            "UPDATE deliveries SET courier_id = $3, state = $4, failure_reason = $5,
                 authorization_revision = $6, failure_detail = $7,
                 expected_pickup_at = $8, expected_delivered_at = $9, version = version + 1
             WHERE id = $1 AND version = $2",
            delivery.id().as_str(),
            version,
            delivery.courier_id().map(CourierId::as_str),
            delivery_state(delivery.state()),
            delivery.failure_reason().map(DeliveryFailureReason::as_str),
            delivery
                .authorization_revision()
                .map(AuthorizationRevision::as_str),
            delivery
                .failure_reason()
                .and_then(DeliveryFailureReason::detail),
            delivery
                .expected_pickup_at()
                .map(ExpectedTime::epoch_seconds),
            delivery
                .expected_delivered_at()
                .map(ExpectedTime::epoch_seconds)
        )
        .execute(connection))?;
        optimistic(updated.rows_affected(), || {
            sql!(self, "SELECT", |connection| sqlx::query_scalar!(
                "SELECT 1 FROM deliveries WHERE id = $1",
                delivery.id().as_str()
            )
            .fetch_optional(connection))
            .map(|row| row.is_some())
        })?;
        let current = delivery.courier_id().map(CourierId::as_str);
        if let Some(courier) = current {
            self.notify_courier(courier)?;
        }
        match previous.flatten() {
            Some(courier) if Some(courier.as_str()) != current => self.notify_courier(&courier),
            _ => Ok(()),
        }
    }
}

impl DeliveryOfferRepository for Store {
    fn get(&self, id: &DeliveryOfferId) -> Result<Option<DeliveryOffer>, RepositoryError> {
        sql!(self, "SELECT", |connection| sqlx::query_as!(
            OfferRow,
            "SELECT id, delivery_id, courier_id, offered_at, expires_at, state, version
             FROM delivery_offers WHERE id = $1",
            id.as_str()
        )
        .fetch_optional(connection))?
        .map(offer_from_row)
        .transpose()
    }

    fn add(&self, offer: &DeliveryOffer) -> Result<(), RepositoryError> {
        let version = db_version(offer.version())?;
        let inserted = sql!(self, "INSERT", |connection| sqlx::query!(
            "INSERT INTO delivery_offers (id, delivery_id, courier_id, offered_at,
                 expires_at, state, version)
             VALUES ($1, $2, $3, $4, $5, $6, $7) ON CONFLICT DO NOTHING",
            offer.id().as_str(),
            offer.delivery_id().as_str(),
            offer.courier_id().as_str(),
            offer.offered_at_epoch_seconds(),
            offer.expires_at_epoch_seconds(),
            offer_state(offer.state()),
            version
        )
        .execute(connection))?;
        if inserted.rows_affected() == 0 {
            return Err(RepositoryError::Conflict);
        }
        self.notify_courier(offer.courier_id().as_str())
    }

    fn save(&self, offer: &DeliveryOffer) -> Result<(), RepositoryError> {
        let version = db_version(offer.version())?;
        let updated = sql!(self, "UPDATE", |connection| sqlx::query!(
            "UPDATE delivery_offers SET state = $3, version = version + 1
             WHERE id = $1 AND version = $2",
            offer.id().as_str(),
            version,
            offer_state(offer.state())
        )
        .execute(connection))?;
        optimistic(updated.rows_affected(), || {
            sql!(self, "SELECT", |connection| sqlx::query_scalar!(
                "SELECT 1 FROM delivery_offers WHERE id = $1",
                offer.id().as_str()
            )
            .fetch_optional(connection))
            .map(|row| row.is_some())
        })?;
        self.notify_courier(offer.courier_id().as_str())
    }
}

impl CourierLocationStore for Store {
    fn record(&self, position: &CourierPosition) -> Result<(), RepositoryError> {
        sql!(self, "INSERT", |connection| sqlx::query!(
            "INSERT INTO courier_positions (courier_id, latitude, longitude, recorded_at)
             VALUES ($1, $2, $3, $4)
             ON CONFLICT (courier_id) DO UPDATE SET latitude = EXCLUDED.latitude,
                 longitude = EXCLUDED.longitude, recorded_at = EXCLUDED.recorded_at",
            position.courier_id.as_str(),
            position.location.latitude(),
            position.location.longitude(),
            position.recorded_at_epoch_seconds
        )
        .execute(connection))
        .map(drop)
    }

    fn latest(&self, courier_id: &CourierId) -> Result<Option<CourierPosition>, RepositoryError> {
        sql!(self, "SELECT", |connection| sqlx::query!(
            "SELECT latitude, longitude, recorded_at FROM courier_positions
             WHERE courier_id = $1",
            courier_id.as_str()
        )
        .fetch_optional(connection))?
        .map(|row| {
            Ok(CourierPosition {
                courier_id: courier_id.clone(),
                location: Location::new(row.latitude, row.longitude)
                    .map_err(|error| corrupt("courier position", error))?,
                recorded_at_epoch_seconds: row.recorded_at,
            })
        })
        .transpose()
    }
}

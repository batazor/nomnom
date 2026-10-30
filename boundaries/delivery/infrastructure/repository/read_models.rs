//! Query read models, the courier positions of the locator and the
//! reconciliation reads of [`Store`].

use crate::infrastructure::locator::{CourierPositions, LocatedCourier};
use crate::infrastructure::reconcile::ReconciliationStore;
use crate::pkg::persistence::{AuthorizationRevision, RepositoryError};
use crate::slices::courier::applications::update_courier_location::{
    ActiveTrackingChannel, CourierPosition,
};
use crate::slices::courier::domains::courier::{CourierId, CourierStatus, Location, MarketId};
use crate::slices::delivery::applications::assignment::get_courier_pool::{
    CourierPoolReadModel, PoolCourierView,
};
use crate::slices::delivery::applications::assignment_process::CandidateDeliveries;
use crate::slices::delivery::applications::offer::list_available_delivery_offers::{
    AvailableDeliveryOfferView, PendingDeliveryOffersReadModel,
};
use crate::slices::delivery::applications::offers_of_delivery::OffersOfDelivery;
use crate::slices::delivery::applications::tracking::get_courier_current_delivery::{
    CourierCurrentDeliveryReadModel, CurrentDeliveryView,
};
use crate::slices::delivery::applications::tracking::get_order_delivery::{
    OrderCourierView, OrderDeliveryReadModel, OrderDeliveryRecord, OrderTrackingRecord,
};
use crate::slices::delivery::domains::delivery::{
    CompoundState, DeliveryId, DeliveryOfferId, OfferState, OrderId, State,
};

use super::rows::{
    active_assignment_states, courier_name, courier_status, delivery_state, delivery_tracking,
    market_id, offer_state, parse_courier_status, parse_delivery_state,
};
use super::{Store, corrupt, domain_version, limit, storage_error};

impl CourierCurrentDeliveryReadModel for Store {
    fn active_delivery_for(
        &self,
        courier_id: &CourierId,
    ) -> Result<Option<CurrentDeliveryView>, RepositoryError> {
        sql!(self, "SELECT", |connection| sqlx::query!(
            "SELECT id, order_id, market_id, state FROM deliveries
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
                market_id: market_id(&row.market_id)?,
                state: parse_delivery_state(&row.state)?,
            })
        })
        .transpose()
    }
}

impl CourierPoolReadModel for Store {
    fn pool_couriers(
        &self,
        market: &MarketId,
        now_epoch_seconds: i64,
    ) -> Result<Vec<PoolCourierView>, RepositoryError> {
        sql!(self, "SELECT", |connection| sqlx::query!(
            r#"SELECT c.id, c.name, c.status, o.id AS "pending_offer_id?",
                 o.order_id AS "offered_order_id?",
                 d.id AS "active_delivery_id?", d.order_id AS "active_order_id?"
             FROM couriers c
             LEFT JOIN LATERAL (
                 SELECT offer.id, offered.order_id FROM delivery_offers offer
                 JOIN deliveries offered ON offered.id = offer.delivery_id
                 WHERE offer.courier_id = c.id AND offer.state = $1 AND offer.expires_at > $2
                 ORDER BY offer.position LIMIT 1) o ON true
             LEFT JOIN LATERAL (
                 SELECT id, order_id FROM deliveries
                 WHERE courier_id = c.id AND state = ANY($3)
                 ORDER BY id LIMIT 1) d ON true
             WHERE c.market_id = $4 AND c.retired_at IS NULL
             ORDER BY c.name, c.id"#,
            offer_state(OfferState::Pending),
            now_epoch_seconds,
            &active_assignment_states()[..] as &[&str],
            market.as_str()
        )
        .fetch_all(connection))?
        .into_iter()
        .map(|row| {
            Ok(PoolCourierView {
                courier_id: CourierId::new(row.id),
                name: courier_name(&row.name)?,
                status: parse_courier_status(&row.status)?,
                pending_offer_id: row.pending_offer_id.map(DeliveryOfferId::new),
                offered_order_id: row.offered_order_id.map(OrderId::new),
                active_delivery_id: row.active_delivery_id.map(DeliveryId::new),
                active_order_id: row.active_order_id.map(OrderId::new),
            })
        })
        .collect()
    }

    fn waiting_deliveries(&self, market: &MarketId) -> Result<u64, RepositoryError> {
        let count = sql!(self, "SELECT", |connection| {
            sqlx::query_scalar!(
                r#"SELECT count(*) AS "count!" FROM deliveries
             WHERE state = $1 AND market_id = $2"#,
                delivery_state(State::Waiting),
                market.as_str()
            )
            .fetch_one(connection)
        })?;
        u64::try_from(count).map_err(|error| corrupt("waiting deliveries count", error))
    }
}

impl OrderDeliveryReadModel for Store {
    fn delivery_of_order(
        &self,
        order_id: &OrderId,
    ) -> Result<OrderDeliveryRecord, RepositoryError> {
        let row = sql!(self, "SELECT", |connection| sqlx::query!(
            r#"SELECT d.state, d.courier_id, c.name AS "name?", d.pickup_x, d.pickup_y,
                 d.dropoff_x, d.dropoff_y, d.tracking_channel,
                 p.latitude AS "latitude?", p.longitude AS "longitude?",
                 p.recorded_at AS "recorded_at?"
             FROM deliveries d
             LEFT JOIN couriers c ON c.id = d.courier_id
             LEFT JOIN courier_positions p ON p.courier_id = d.courier_id
             WHERE d.order_id = $1"#,
            order_id.as_str()
        )
        .fetch_optional(connection))?;
        let Some(row) = row else {
            return Ok(OrderDeliveryRecord::default());
        };
        let state = parse_delivery_state(&row.state)?;
        // The courier stays after the handover (FR-626).
        let shows_courier =
            state.is_in(CompoundState::ActiveAssignment) || state == State::Delivered;
        let courier = match (
            shows_courier,
            row.courier_id.as_deref(),
            row.name.as_deref(),
        ) {
            (true, Some(id), Some(name)) => Some(OrderCourierView {
                courier_id: CourierId::new(id),
                name: courier_name(name)?,
            }),
            _ => None,
        };
        let stored_tracking = delivery_tracking(
            (row.pickup_x, row.pickup_y),
            (row.dropoff_x, row.dropoff_y),
            row.tracking_channel,
        )?;
        let tracking = match (
            state.is_in(CompoundState::ActiveAssignment),
            stored_tracking,
        ) {
            (true, Some(tracking)) => {
                let courier_position =
                    match (row.courier_id, row.latitude, row.longitude, row.recorded_at) {
                        (Some(id), Some(latitude), Some(longitude), Some(recorded_at)) => {
                            Some(CourierPosition {
                                courier_id: CourierId::new(id),
                                location: Location::new(latitude, longitude)
                                    .map_err(|error| corrupt("courier position", error))?,
                                recorded_at_epoch_seconds: recorded_at,
                            })
                        }
                        _ => None,
                    };
                Some(OrderTrackingRecord {
                    channel: tracking.channel,
                    pickup: tracking.pickup,
                    dropoff: tracking.dropoff,
                    courier_position,
                })
            }
            _ => None,
        };
        Ok(OrderDeliveryRecord { courier, tracking })
    }
}

impl ActiveTrackingChannel for Store {
    fn active_channel(&self, courier_id: &CourierId) -> Result<Option<String>, RepositoryError> {
        // A courier has at most one active delivery (FR-310).
        sql!(self, "SELECT", |connection| sqlx::query_scalar!(
            r#"SELECT tracking_channel AS "tracking_channel!" FROM deliveries
             WHERE courier_id = $1 AND state = ANY($2) AND tracking_channel IS NOT NULL"#,
            courier_id.as_str(),
            &active_assignment_states()[..] as &[&str]
        )
        .fetch_optional(connection))
    }
}

impl CandidateDeliveries for Store {
    fn oldest_waiting_for(
        &self,
        courier_id: &CourierId,
    ) -> Result<Option<DeliveryId>, RepositoryError> {
        // A Delivery stored before created_at existed counts as created at 0;
        // ids are "C" collated, as in the waiting index.
        sql!(self, "SELECT", |connection| sqlx::query_scalar!(
            "SELECT d.id FROM deliveries d JOIN couriers c ON c.market_id = d.market_id
             WHERE c.id = $1 AND d.state = $2
               AND NOT EXISTS (SELECT 1 FROM delivery_offers o
                               WHERE o.delivery_id = d.id
                                 AND (o.state = $3 OR o.courier_id = $1))
             ORDER BY COALESCE(d.created_at, 0), d.id LIMIT 1",
            courier_id.as_str(),
            delivery_state(State::Waiting),
            offer_state(OfferState::Pending)
        )
        .fetch_optional(connection))
        .map(|id| id.map(DeliveryId::new))
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

/// Last position of a courier with its stored Market.
fn located_courier(
    courier_id: String,
    market: &str,
    latitude: f64,
    longitude: f64,
    recorded_at: i64,
) -> Result<LocatedCourier, RepositoryError> {
    Ok(LocatedCourier {
        position: CourierPosition {
            courier_id: CourierId::new(courier_id),
            location: Location::new(latitude, longitude)
                .map_err(|error| corrupt("courier position", error))?,
            recorded_at_epoch_seconds: recorded_at,
        },
        market_id: market_id(market)?,
    })
}

impl CourierPositions for Store {
    // Candidate search of ADR 41: available couriers of the Delivery's Market.
    fn available_positions(
        &self,
        market: &MarketId,
    ) -> Result<Vec<LocatedCourier>, RepositoryError> {
        sql!(self, "SELECT", |connection| sqlx::query!(
            "SELECT p.courier_id, c.market_id, p.latitude, p.longitude, p.recorded_at
             FROM courier_positions p JOIN couriers c ON c.id = p.courier_id
             WHERE c.market_id = $1 AND c.status = $2
             ORDER BY p.courier_id",
            market.as_str(),
            courier_status(CourierStatus::Available)
        )
        .fetch_all(connection))?
        .into_iter()
        .map(|row| {
            located_courier(
                row.courier_id,
                &row.market_id,
                row.latitude,
                row.longitude,
                row.recorded_at,
            )
        })
        .collect()
    }

    fn located(&self, courier_id: &CourierId) -> Result<Option<LocatedCourier>, RepositoryError> {
        sql!(self, "SELECT", |connection| sqlx::query!(
            "SELECT p.courier_id, c.market_id, p.latitude, p.longitude, p.recorded_at
             FROM courier_positions p JOIN couriers c ON c.id = p.courier_id
             WHERE p.courier_id = $1",
            courier_id.as_str()
        )
        .fetch_optional(connection))?
        .map(|row| {
            located_courier(
                row.courier_id,
                &row.market_id,
                row.latitude,
                row.longitude,
                row.recorded_at,
            )
        })
        .transpose()
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

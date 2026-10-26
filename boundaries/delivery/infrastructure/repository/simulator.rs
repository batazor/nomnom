//! Simulator switch and the Simulator work read model of [`Store`]
//! (FR-604, FR-605).

use crate::pkg::persistence::RepositoryError;
use crate::slices::courier::domains::courier::CourierId;
use crate::slices::delivery::domains::delivery::{DeliveryId, DeliveryOfferId, OfferState, State};
use crate::slices::simulator::applications::work::{
    ActiveDeliveryWork, DeliveryStep, PendingOfferWork, SimulatorWork,
};
use crate::slices::simulator::domains::simulator::SimulatorStateRepository;

use super::rows::{active_assignment_states, offer_state, parse_delivery_state};
use super::{Store, corrupt, storage_error};

impl SimulatorStateRepository for Store {
    fn enabled(&self) -> Result<bool, RepositoryError> {
        sql!(self, "SELECT", |connection| sqlx::query_scalar!(
            "SELECT enabled FROM simulator"
        )
        .fetch_optional(connection))
        .map(|enabled| enabled == Some(true))
    }

    fn set_enabled(&self, enabled: bool) -> Result<(), RepositoryError> {
        sql!(self, "INSERT", |connection| sqlx::query!(
            "INSERT INTO simulator (singleton, enabled) VALUES (TRUE, $1)
             ON CONFLICT (singleton) DO UPDATE SET enabled = EXCLUDED.enabled",
            enabled
        )
        .execute(connection))
        .map(|_| ())
    }
}

impl SimulatorWork for Store {
    fn pending_offers(&self) -> Result<Vec<PendingOfferWork>, RepositoryError> {
        sql!(self, "SELECT", |connection| sqlx::query!(
            "SELECT id, courier_id, expires_at FROM delivery_offers
             WHERE state = $1 ORDER BY position",
            offer_state(OfferState::Pending)
        )
        .fetch_all(connection))
        .map(|rows| {
            rows.into_iter()
                .map(|row| PendingOfferWork {
                    offer_id: DeliveryOfferId::new(row.id),
                    courier_id: CourierId::new(row.courier_id),
                    expires_at_epoch_seconds: row.expires_at,
                })
                .collect()
        })
    }

    fn active_deliveries(&self) -> Result<Vec<ActiveDeliveryWork>, RepositoryError> {
        // `courier_id IS NOT NULL` narrows the nullable column: `courier_id!`.
        sql!(self, "SELECT", |connection| sqlx::query!(
            r#"SELECT id, courier_id AS "courier_id!", state FROM deliveries
             WHERE state = ANY($1) AND courier_id IS NOT NULL ORDER BY id"#,
            &active_assignment_states()[..] as &[&str]
        )
        .fetch_all(connection))?
        .into_iter()
        .map(|row| {
            let state = parse_delivery_state(&row.state)?;
            let step = match state {
                State::Assigned => DeliveryStep::Pickup,
                State::PickedUp => DeliveryStep::Handover,
                other => return Err(corrupt("active delivery state", format!("{other:?}"))),
            };
            Ok(ActiveDeliveryWork {
                delivery_id: DeliveryId::new(row.id),
                courier_id: CourierId::new(row.courier_id),
                step,
            })
        })
        .collect()
    }
}

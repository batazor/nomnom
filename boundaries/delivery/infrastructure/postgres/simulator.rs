//! Simulator switch and the Simulator work read model of [`PostgresStore`]
//! (FR-604, FR-605).

use crate::pkg::persistence::RepositoryError;
use crate::slices::courier::domains::courier::CourierId;
use crate::slices::delivery::domains::delivery::{DeliveryId, DeliveryOfferId, OfferState, State};
use crate::slices::simulator::applications::work::{
    ActiveDeliveryWork, DeliveryStep, PendingOfferWork, SimulatorWork,
};
use crate::slices::simulator::domains::simulator::SimulatorStateRepository;

use super::rows::{column, delivery_state, offer_state, parse_delivery_state};
use super::{PostgresStore, corrupt, storage_error};

impl SimulatorStateRepository for PostgresStore {
    fn enabled(&self) -> Result<bool, RepositoryError> {
        sql!(self, "SELECT", |connection| sqlx::query_scalar::<_, bool>(
            "SELECT enabled FROM simulator"
        )
        .fetch_optional(connection))
        .map(|enabled| enabled == Some(true))
    }

    fn set_enabled(&self, enabled: bool) -> Result<(), RepositoryError> {
        sql!(self, "INSERT", |connection| sqlx::query(
            "INSERT INTO simulator (singleton, enabled) VALUES (TRUE, $1)
             ON CONFLICT (singleton) DO UPDATE SET enabled = EXCLUDED.enabled"
        )
        .bind(enabled)
        .execute(connection))
        .map(|_| ())
    }
}

impl SimulatorWork for PostgresStore {
    fn pending_offers(&self) -> Result<Vec<PendingOfferWork>, RepositoryError> {
        sql!(self, "SELECT", |connection| sqlx::query(
            "SELECT id, courier_id, expires_at FROM delivery_offers
             WHERE state = $1 ORDER BY position"
        )
        .bind(offer_state(OfferState::Pending))
        .fetch_all(connection))?
        .iter()
        .map(|row| {
            Ok(PendingOfferWork {
                offer_id: DeliveryOfferId::new(column::<String>(row, "id")?),
                courier_id: CourierId::new(column::<String>(row, "courier_id")?),
                expires_at_epoch_seconds: column(row, "expires_at")?,
            })
        })
        .collect()
    }

    fn active_deliveries(&self) -> Result<Vec<ActiveDeliveryWork>, RepositoryError> {
        let states = [
            delivery_state(State::Assigned),
            delivery_state(State::PickedUp),
        ];
        sql!(self, "SELECT", |connection| sqlx::query(
            "SELECT id, courier_id, state FROM deliveries
             WHERE state = ANY($1) AND courier_id IS NOT NULL ORDER BY id"
        )
        .bind(&states[..])
        .fetch_all(connection))?
        .iter()
        .map(|row| {
            let state = parse_delivery_state(column(row, "state")?)?;
            let step = match state {
                State::Assigned => DeliveryStep::Pickup,
                State::PickedUp => DeliveryStep::Handover,
                other => return Err(corrupt("active delivery state", format!("{other:?}"))),
            };
            Ok(ActiveDeliveryWork {
                delivery_id: DeliveryId::new(column::<String>(row, "id")?),
                courier_id: CourierId::new(column::<String>(row, "courier_id")?),
                step,
            })
        })
        .collect()
    }
}

//! Работа Simulator: сущности всех курьеров, ожидающие действия курьера
//! (FR-605). Глобальная выборка без subject — её читает только фоновый цикл
//! процесса; сами действия идут через use cases с subject курьера.

use crate::pkg::persistence::RepositoryError;
use crate::slices::courier::domains::courier::CourierId;
use crate::slices::delivery::domains::delivery::{DeliveryId, DeliveryOfferId};

/// Pending offer, адресованный курьеру.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct PendingOfferWork {
    pub offer_id: DeliveryOfferId,
    pub courier_id: CourierId,
    pub expires_at_epoch_seconds: i64,
}

/// Шаг доставки, который ждёт назначенного курьера.
#[derive(Clone, Copy, Debug, Eq, Hash, PartialEq)]
pub enum DeliveryStep {
    /// `Assigned`: курьер забирает заказ у ресторана.
    Pickup,
    /// `PickedUp`: курьер передаёт заказ клиенту.
    Handover,
}

/// Доставка в `Assigned` или `PickedUp` с назначенным курьером.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct ActiveDeliveryWork {
    pub delivery_id: DeliveryId,
    pub courier_id: CourierId,
    pub step: DeliveryStep,
}

/// Read model работы Simulator; реализует PostgreSQL.
pub trait SimulatorWork {
    fn pending_offers(&self) -> Result<Vec<PendingOfferWork>, RepositoryError>;

    fn active_deliveries(&self) -> Result<Vec<ActiveDeliveryWork>, RepositoryError>;
}

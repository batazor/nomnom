//! Работа Simulator: сущности всех курьеров, ожидающие действия курьера
//! (FR-605). Глобальная выборка без subject — её читает только фоновый цикл
//! процесса; сами действия идут через use cases с subject курьера.

use crate::pkg::persistence::RepositoryError;
use crate::slices::courier::domains::courier::{CourierId, Location};
use crate::slices::delivery::domains::delivery::{DeliveryId, DeliveryOfferId, Intersection};

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

/// Точки доставки в Virtual City (FR-659).
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct DeliveryPoints {
    pub pickup: Intersection,
    pub dropoff: Intersection,
}

/// Доставка в `Assigned` или `PickedUp` с назначенным курьером.
#[derive(Clone, Debug, PartialEq)]
pub struct ActiveDeliveryWork {
    pub delivery_id: DeliveryId,
    pub courier_id: CourierId,
    pub step: DeliveryStep,
    /// `None` — доставка создана до точек, Simulator её не двигает (FR-662).
    pub points: Option<DeliveryPoints>,
    /// Сохранённая позиция курьера; `None` — неизвестна.
    pub courier_location: Option<Location>,
}

/// Read model работы Simulator; реализует PostgreSQL.
pub trait SimulatorWork {
    fn pending_offers(&self) -> Result<Vec<PendingOfferWork>, RepositoryError>;

    fn active_deliveries(&self) -> Result<Vec<ActiveDeliveryWork>, RepositoryError>;
}

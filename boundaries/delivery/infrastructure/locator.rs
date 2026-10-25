//! Локатор курьеров production-сборки (FR-542): кандидаты — доступные курьеры
//! с последней сохранённой позицией из `CourierLocationStore`. Правило выбора
//! кандидата (`find_courier::select_candidate`) не меняется.
//!
//! Адрес pickup пока не геокодируется (`Address` — строка), поэтому
//! расстояние и зона неизвестны: курьер с известной позицией считается в зоне
//! на расстоянии 0, и выбор среди равных решает id курьера. Статический
//! локатор `infrastructure::memory` — только для тестов.

use std::sync::Arc;

use crate::pkg::persistence::RepositoryError;
use crate::slices::courier::applications::update_courier_location::{
    CourierLocationStore, CourierPosition,
};
use crate::slices::courier::domains::courier::CourierId;
use crate::slices::delivery::applications::courier_locator::{
    CourierCandidate, CourierLocator, LocatorError,
};
use crate::slices::delivery::domains::delivery::Address;

/// Роль хранилища, которую читает [`StoredCourierLocator`].
pub trait CourierPositions: CourierLocationStore + Send + Sync {
    /// Последние позиции курьеров в состоянии `available`, по id курьера.
    fn available_positions(&self) -> Result<Vec<CourierPosition>, RepositoryError>;
}

/// [`CourierLocator`] поверх сохранённых позиций курьеров.
pub struct StoredCourierLocator {
    positions: Arc<dyn CourierPositions>,
}

impl StoredCourierLocator {
    #[must_use]
    pub fn new(positions: Arc<dyn CourierPositions>) -> Self {
        Self { positions }
    }
}

impl CourierLocator for StoredCourierLocator {
    fn candidates_near(&self, _pickup: &Address) -> Result<Vec<CourierCandidate>, LocatorError> {
        Ok(self
            .positions
            .available_positions()
            .map_err(unavailable)?
            .into_iter()
            .map(|position| candidate(position.courier_id))
            .collect())
    }

    fn snapshot(
        &self,
        courier_id: &CourierId,
        _pickup: &Address,
    ) -> Result<Option<CourierCandidate>, LocatorError> {
        Ok(self
            .positions
            .latest(courier_id)
            .map_err(unavailable)?
            .map(|position| candidate(position.courier_id)))
    }
}

/// Без геокодирования pickup расстояние и зона неизвестны; занятость курьера
/// проверяет `CourierAssignment`, поэтому активных назначений 0.
fn candidate(courier_id: CourierId) -> CourierCandidate {
    CourierCandidate {
        courier_id,
        within_delivery_zone: true,
        distance_to_pickup_meters: 0,
        active_assignments: 0,
    }
}

fn unavailable(error: RepositoryError) -> LocatorError {
    LocatorError::Unavailable(error.to_string())
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::infrastructure::postgres::test_support::TestStore;
    use crate::slices::courier::domains::courier::{
        Courier, CourierRepository, CourierStatus, Location,
    };

    fn pickup() -> Address {
        Address::new("Restaurant street 1").expect("pickup")
    }

    fn courier(store: &TestStore, id: &str, status: CourierStatus, positioned: bool) {
        CourierRepository::add(store, &Courier::restore(CourierId::new(id), status))
            .expect("courier");
        if positioned {
            store
                .record(&CourierPosition {
                    courier_id: CourierId::new(id),
                    location: Location::new(52.37, 4.89).expect("location"),
                    recorded_at_epoch_seconds: 1_000,
                })
                .expect("position");
        }
    }

    #[test]
    fn candidates_are_available_couriers_with_a_position() {
        let store = Arc::new(TestStore::new());
        courier(&store, "courier-2", CourierStatus::Available, true);
        courier(&store, "courier-1", CourierStatus::Available, true);
        courier(&store, "busy", CourierStatus::Busy, true);
        courier(&store, "off", CourierStatus::Unavailable, true);
        courier(&store, "unpositioned", CourierStatus::Available, false);
        let locator = StoredCourierLocator::new(store);

        let candidates = locator.candidates_near(&pickup()).expect("candidates");

        assert_eq!(
            candidates,
            vec![
                candidate(CourierId::new("courier-1")),
                candidate(CourierId::new("courier-2")),
            ]
        );
        assert_eq!(
            locator.snapshot(&CourierId::new("courier-1"), &pickup()),
            Ok(Some(candidate(CourierId::new("courier-1"))))
        );
        assert_eq!(
            locator.snapshot(&CourierId::new("unpositioned"), &pickup()),
            Ok(None)
        );
    }

    #[test]
    fn storage_outage_is_locator_unavailable() {
        let store = Arc::new(TestStore::new());
        store.set_unavailable(true);
        let locator = StoredCourierLocator::new(store);

        assert!(matches!(
            locator.candidates_near(&pickup()),
            Err(LocatorError::Unavailable(_))
        ));
    }
}

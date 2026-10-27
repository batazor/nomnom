//! Локатор курьеров production-сборки (FR-542): кандидаты — доступные курьеры
//! Market доставки (ADR 41) с последней сохранённой позицией из
//! `CourierLocationStore`; Market кандидата — сохранённый Market курьера. Правило выбора
//! кандидата (`find_courier::select_candidate`) не меняется.
//!
//! Delivery не хранит место pickup, поэтому расстояние и зона неизвестны: курьер с известной позицией считается в зоне
//! на расстоянии 0, и выбор среди равных решает id курьера. Статический
//! локатор `infrastructure::memory` — только для тестов.

use std::sync::Arc;

use crate::pkg::persistence::RepositoryError;
use crate::slices::courier::applications::update_courier_location::{
    CourierLocationStore, CourierPosition,
};
use crate::slices::courier::domains::courier::{CourierId, MarketId};
use crate::slices::delivery::applications::courier_locator::{
    CourierCandidate, CourierLocator, LocatorError,
};

/// Последняя позиция курьера с его Market.
#[derive(Clone, Debug, PartialEq)]
pub struct LocatedCourier {
    pub position: CourierPosition,
    pub market_id: MarketId,
}

/// Роль хранилища, которую читает [`StoredCourierLocator`].
pub trait CourierPositions: CourierLocationStore + Send + Sync {
    /// Последние позиции курьеров Market `market_id` в состоянии `available`,
    /// по id курьера.
    fn available_positions(
        &self,
        market_id: &MarketId,
    ) -> Result<Vec<LocatedCourier>, RepositoryError>;

    /// Последняя позиция зарегистрированного курьера; `None` — позиция
    /// неизвестна.
    fn located(&self, courier_id: &CourierId) -> Result<Option<LocatedCourier>, RepositoryError>;
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
    fn candidates(&self, market_id: &MarketId) -> Result<Vec<CourierCandidate>, LocatorError> {
        Ok(self
            .positions
            .available_positions(market_id)
            .map_err(unavailable)?
            .into_iter()
            .map(candidate)
            .collect())
    }

    fn snapshot(&self, courier_id: &CourierId) -> Result<Option<CourierCandidate>, LocatorError> {
        Ok(self
            .positions
            .located(courier_id)
            .map_err(unavailable)?
            .map(candidate))
    }
}

/// Без места pickup расстояние и зона неизвестны; занятость курьера
/// проверяет `CourierAssignment`, поэтому активных назначений 0.
fn candidate(located: LocatedCourier) -> CourierCandidate {
    CourierCandidate {
        courier_id: located.position.courier_id,
        market_id: located.market_id,
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
    use crate::infrastructure::repository::test_support::TestStore;
    use crate::slices::courier::domains::courier::{
        Courier, CourierName, CourierRepository, CourierStatus, Location,
    };

    fn market(value: &str) -> MarketId {
        MarketId::new(value).expect("market id")
    }

    fn courier(store: &TestStore, id: &str, status: CourierStatus, positioned: bool) {
        courier_in(store, id, status, positioned, "berlin");
    }

    fn courier_in(
        store: &TestStore,
        id: &str,
        status: CourierStatus,
        positioned: bool,
        market_id: &str,
    ) {
        CourierRepository::add(
            store,
            &Courier::restore(
                CourierId::new(id),
                CourierName::new("Timur").expect("courier name"),
                market(market_id),
                status,
            ),
        )
        .expect("courier");
        if positioned {
            store
                .record(
                    &CourierPosition {
                        courier_id: CourierId::new(id),
                        location: Location::new(52.37, 4.89).expect("location"),
                        recorded_at_epoch_seconds: 1_000,
                    },
                    0,
                )
                .expect("position");
        }
    }

    fn expected(id: &str, market_id: &str) -> CourierCandidate {
        CourierCandidate {
            courier_id: CourierId::new(id),
            market_id: market(market_id),
            within_delivery_zone: true,
            distance_to_pickup_meters: 0,
            active_assignments: 0,
        }
    }

    /// ADR 41: candidates are the available couriers of the Delivery's
    /// Market only.
    #[test]
    fn candidates_are_available_couriers_of_the_market_with_a_position() {
        let store = Arc::new(TestStore::new());
        courier_in(
            &store,
            "hamburg-1",
            CourierStatus::Available,
            true,
            "hamburg",
        );
        courier(&store, "courier-2", CourierStatus::Available, true);
        courier(&store, "courier-1", CourierStatus::Available, true);
        courier(&store, "busy", CourierStatus::Busy, true);
        courier(&store, "off", CourierStatus::Unavailable, true);
        courier(&store, "unpositioned", CourierStatus::Available, false);
        let locator = StoredCourierLocator::new(store);

        let candidates = locator.candidates(&market("berlin")).expect("candidates");

        assert_eq!(
            candidates,
            vec![
                expected("courier-1", "berlin"),
                expected("courier-2", "berlin")
            ]
        );
        assert_eq!(
            locator.candidates(&market("hamburg")),
            Ok(vec![expected("hamburg-1", "hamburg")])
        );
        assert_eq!(
            locator.snapshot(&CourierId::new("courier-1")),
            Ok(Some(expected("courier-1", "berlin")))
        );
        assert_eq!(
            locator.snapshot(&CourierId::new("hamburg-1")),
            Ok(Some(expected("hamburg-1", "hamburg")))
        );
        assert_eq!(locator.snapshot(&CourierId::new("unpositioned")), Ok(None));
    }

    #[test]
    fn storage_outage_is_locator_unavailable() {
        let store = Arc::new(TestStore::new());
        store.set_unavailable(true);
        let locator = StoredCourierLocator::new(store);

        assert!(matches!(
            locator.candidates(&market("berlin")),
            Err(LocatorError::Unavailable(_))
        ));
    }
}

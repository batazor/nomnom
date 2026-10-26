//! Update Courier Location: the courier application, or a dispatcher placing
//! a new courier of the Courier Pool (FR-620), records the last known
//! position of a registered courier. Frequency, accuracy and retention are
//! not constrained by this use case.

use crate::pkg::application::{Clock, Subject};
use crate::pkg::persistence::RepositoryError;
use crate::slices::courier::domains::courier::{CourierId, CourierRepository, Location};

use super::authorization::{CourierAuthorization, CourierPermission, check_courier_or_dispatcher};
use super::courier_id;
use super::error::{CourierApplicationError, InvalidCourierRequest};

/// Last known position of a courier with the `delivery` time it was recorded.
#[derive(Clone, Debug, PartialEq)]
pub struct CourierPosition {
    pub courier_id: CourierId,
    pub location: Location,
    pub recorded_at_epoch_seconds: i64,
}

/// High-frequency store of the last known courier position. A single write,
/// no Unit of Work.
pub trait CourierLocationStore {
    /// Replaces the last known position of the courier.
    fn record(&self, position: &CourierPosition) -> Result<(), RepositoryError>;

    fn latest(&self, courier_id: &CourierId) -> Result<Option<CourierPosition>, RepositoryError>;
}

#[derive(Clone, Debug, PartialEq)]
pub struct Input {
    pub courier_id: String,
    pub latitude: f64,
    pub longitude: f64,
}

#[derive(Clone, Debug, PartialEq)]
pub struct Output {
    pub position: CourierPosition,
}

pub struct Handler<'a> {
    pub authorization: &'a dyn CourierAuthorization,
    pub couriers: &'a dyn CourierRepository,
    pub locations: &'a dyn CourierLocationStore,
    pub clock: &'a dyn Clock,
}

impl Handler<'_> {
    pub fn execute(
        &self,
        subject: &Subject,
        input: Input,
    ) -> Result<Output, CourierApplicationError> {
        let courier_id = courier_id(&input.courier_id)?;
        let location = Location::new(input.latitude, input.longitude).map_err(|error| {
            CourierApplicationError::InvalidRequest(InvalidCourierRequest::Location(error))
        })?;
        check_courier_or_dispatcher(
            self.authorization,
            subject,
            CourierPermission::UpdateCourierLocation,
            &courier_id,
        )?;

        if self.couriers.get(&courier_id)?.is_none() {
            return Err(CourierApplicationError::CourierNotFound(courier_id));
        }
        let position = CourierPosition {
            courier_id,
            location,
            recorded_at_epoch_seconds: self.clock.now_epoch_seconds(),
        };
        self.locations.record(&position)?;
        Ok(Output { position })
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::infrastructure::memory::{ManualClock, StaticAuthorization};
    use crate::infrastructure::repository::test_support::TestStore;
    use crate::slices::courier::applications::authorization::CourierResource;
    use crate::slices::courier::domains::courier::MarketId;
    use crate::slices::courier::domains::courier::{
        Courier, CourierName, CourierStatus, LocationError,
    };

    fn subject() -> Subject {
        Subject::User("courier-1".to_owned())
    }

    fn input(latitude: f64, longitude: f64) -> Input {
        Input {
            courier_id: "courier-1".to_owned(),
            latitude,
            longitude,
        }
    }

    fn seeded() -> TestStore {
        let store = TestStore::new();
        store
            .add(&Courier::restore(
                CourierId::new("courier-1"),
                CourierName::new("Timur").expect("courier name"),
                MarketId::new("berlin").expect("market id"),
                CourierStatus::Unavailable,
            ))
            .expect("seed courier");
        store
    }

    /// FR-620: a dispatcher places a new courier of the Courier Pool.
    #[test]
    fn dispatcher_records_the_position_of_any_courier() {
        let store = seeded();
        let dispatcher = Subject::User("dispatcher".to_owned());
        let authorization = StaticAuthorization::grants_only();
        authorization.grant_courier(
            dispatcher.clone(),
            CourierPermission::ManageCouriers,
            CourierResource::Platform,
        );
        let clock = ManualClock::new(1_000);
        let handler = Handler {
            authorization: &authorization,
            couriers: &store,
            locations: &store,
            clock: &clock,
        };

        handler
            .execute(&dispatcher, input(52.52, 13.405))
            .expect("dispatcher records the position");

        assert!(
            store
                .latest(&CourierId::new("courier-1"))
                .expect("read position")
                .is_some()
        );
    }

    /// AC-307
    #[test]
    fn records_latest_position_with_clock_time() {
        let store = seeded();
        let authorization = StaticAuthorization::allow_all();
        let clock = ManualClock::new(1_000);
        let handler = Handler {
            authorization: &authorization,
            couriers: &store,
            locations: &store,
            clock: &clock,
        };

        handler.execute(&subject(), input(1.0, 2.0)).expect("first");
        clock.advance(5);
        let output = handler
            .execute(&subject(), input(52.52, 13.405))
            .expect("second");

        let expected = CourierPosition {
            courier_id: CourierId::new("courier-1"),
            location: Location::new(52.52, 13.405).expect("location"),
            recorded_at_epoch_seconds: 1_005,
        };
        assert_eq!(output.position, expected);
        assert_eq!(
            store.latest(&CourierId::new("courier-1")),
            Ok(Some(expected))
        );
        assert_eq!(
            store
                .get(&CourierId::new("courier-1"))
                .expect("read")
                .expect("courier")
                .status(),
            CourierStatus::Unavailable
        );
    }

    /// AC-308
    #[test]
    fn out_of_range_coordinates_are_rejected_without_changes() {
        let store = seeded();
        let authorization = StaticAuthorization::allow_all();
        let clock = ManualClock::new(1_000);
        let handler = Handler {
            authorization: &authorization,
            couriers: &store,
            locations: &store,
            clock: &clock,
        };
        handler.execute(&subject(), input(1.0, 2.0)).expect("seed");

        assert_eq!(
            handler.execute(&subject(), input(91.0, 2.0)),
            Err(CourierApplicationError::InvalidRequest(
                InvalidCourierRequest::Location(LocationError::LatitudeOutOfRange)
            ))
        );
        assert_eq!(
            store
                .latest(&CourierId::new("courier-1"))
                .expect("read")
                .map(|position| position.location),
            Some(Location::new(1.0, 2.0).expect("location"))
        );
    }

    /// AC-309
    #[test]
    fn unknown_courier_is_not_found() {
        let store = TestStore::new();
        let authorization = StaticAuthorization::allow_all();
        let clock = ManualClock::new(1_000);
        let handler = Handler {
            authorization: &authorization,
            couriers: &store,
            locations: &store,
            clock: &clock,
        };

        assert_eq!(
            handler.execute(&subject(), input(1.0, 2.0)),
            Err(CourierApplicationError::CourierNotFound(CourierId::new(
                "courier-1"
            )))
        );
        assert_eq!(store.latest(&CourierId::new("courier-1")), Ok(None));
    }

    #[test]
    fn denied_subject_does_not_record_position() {
        let store = seeded();
        let authorization = StaticAuthorization::deny_all();
        let clock = ManualClock::new(1_000);
        let handler = Handler {
            authorization: &authorization,
            couriers: &store,
            locations: &store,
            clock: &clock,
        };

        assert_eq!(
            handler.execute(&subject(), input(1.0, 2.0)),
            Err(CourierApplicationError::PermissionDenied)
        );
        assert_eq!(store.latest(&CourierId::new("courier-1")), Ok(None));
    }
}

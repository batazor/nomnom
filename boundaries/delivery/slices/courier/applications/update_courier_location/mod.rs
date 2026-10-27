//! Update Courier Location: the courier application, or a dispatcher placing
//! a new courier of the Courier Pool (FR-620), records the last known
//! position of a registered courier (FR-303). The stored position is thinned:
//! it is written only when the stored one is older than the persist
//! interval. Every accepted position of a courier with an active delivery is
//! published to the Tracking Channel of that delivery (FR-661) — a live
//! stream without Integration Events or Outbox.

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
    /// Replaces the last known position only when the stored one was
    /// recorded at least `min_interval_seconds` before `position`, or none is
    /// stored; returns whether it was written. `0` always writes.
    fn record(
        &self,
        position: &CourierPosition,
        min_interval_seconds: i64,
    ) -> Result<bool, RepositoryError>;

    fn latest(&self, courier_id: &CourierId) -> Result<Option<CourierPosition>, RepositoryError>;
}

/// Tracking Channel of the active delivery (assigned or picked up) of a
/// courier. The `courier` slice does not know `Delivery` (FR-486): a
/// top-level adapter reads it.
pub trait ActiveTrackingChannel {
    /// `None` without an active delivery or for a delivery without a channel.
    fn active_channel(&self, courier_id: &CourierId) -> Result<Option<String>, RepositoryError>;
}

/// Live stream of courier positions: publishes a position to a Tracking
/// Channel. A failure is [`RepositoryError::Unavailable`].
pub trait CourierPositionPublisher {
    fn publish(&self, channel: &str, position: &CourierPosition) -> Result<(), RepositoryError>;
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
    pub channels: &'a dyn ActiveTrackingChannel,
    pub publisher: &'a dyn CourierPositionPublisher,
    pub clock: &'a dyn Clock,
    /// `TRACKING__PERSIST_INTERVAL` in seconds.
    pub persist_interval_seconds: i64,
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
        self.locations
            .record(&position, self.persist_interval_seconds)?;
        if let Some(channel) = self.channels.active_channel(&position.courier_id)? {
            self.publisher.publish(&channel, &position)?;
        }
        Ok(Output { position })
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::infrastructure::memory::{
        ManualClock, RecordingPositionPublisher, StaticAuthorization,
    };
    use crate::infrastructure::repository::test_support::TestStore;
    use crate::slices::courier::applications::authorization::CourierResource;
    use crate::slices::courier::domains::courier::MarketId;
    use crate::slices::courier::domains::courier::{
        Courier, CourierName, CourierStatus, LocationError,
    };

    /// `TRACKING__PERSIST_INTERVAL` of the tests.
    const PERSIST_INTERVAL: i64 = 15;

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

    fn handler<'a>(
        store: &'a TestStore,
        authorization: &'a StaticAuthorization,
        publisher: &'a RecordingPositionPublisher,
        clock: &'a ManualClock,
    ) -> Handler<'a> {
        Handler {
            authorization,
            couriers: store,
            locations: store,
            channels: store,
            publisher,
            clock,
            persist_interval_seconds: PERSIST_INTERVAL,
        }
    }

    fn position(latitude: f64, longitude: f64, recorded_at: i64) -> CourierPosition {
        CourierPosition {
            courier_id: CourierId::new("courier-1"),
            location: Location::new(latitude, longitude).expect("location"),
            recorded_at_epoch_seconds: recorded_at,
        }
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
        let publisher = RecordingPositionPublisher::new();
        let clock = ManualClock::new(1_000);

        handler(&store, &authorization, &publisher, &clock)
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
        let publisher = RecordingPositionPublisher::new();
        let clock = ManualClock::new(1_000);
        let handler = handler(&store, &authorization, &publisher, &clock);

        handler.execute(&subject(), input(1.0, 2.0)).expect("first");
        clock.advance(PERSIST_INTERVAL);
        let output = handler
            .execute(&subject(), input(52.52, 13.405))
            .expect("second");

        let expected = position(52.52, 13.405, 1_015);
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

    /// AC-883: a recent stored position is kept, the new one is still
    /// published.
    #[test]
    fn recent_stored_position_is_kept_and_new_one_is_published() {
        let store = seeded();
        store.assigned_delivery_with_channel("courier-1", "ab".repeat(16).as_str());
        let authorization = StaticAuthorization::allow_all();
        let publisher = RecordingPositionPublisher::new();
        let clock = ManualClock::new(1_000);
        let handler = handler(&store, &authorization, &publisher, &clock);

        handler.execute(&subject(), input(1.0, 2.0)).expect("first");
        clock.advance(3);
        handler
            .execute(&subject(), input(3.0, 4.0))
            .expect("second");

        assert_eq!(
            store.latest(&CourierId::new("courier-1")),
            Ok(Some(position(1.0, 2.0, 1_000)))
        );
        assert_eq!(
            publisher.published(),
            vec![
                ("ab".repeat(16), position(1.0, 2.0, 1_000)),
                ("ab".repeat(16), position(3.0, 4.0, 1_003)),
            ]
        );
    }

    /// AC-878
    #[test]
    fn courier_without_delivery_publishes_nothing() {
        let store = seeded();
        let authorization = StaticAuthorization::allow_all();
        let publisher = RecordingPositionPublisher::new();
        let clock = ManualClock::new(1_000);

        handler(&store, &authorization, &publisher, &clock)
            .execute(&subject(), input(1.0, 2.0))
            .expect("recorded");

        assert_eq!(publisher.published(), Vec::new());
    }

    /// AC-879
    #[test]
    fn publication_failure_is_unavailable() {
        let store = seeded();
        store.assigned_delivery_with_channel("courier-1", "ab".repeat(16).as_str());
        let authorization = StaticAuthorization::allow_all();
        let publisher = RecordingPositionPublisher::failing("valkey down");
        let clock = ManualClock::new(1_000);

        assert_eq!(
            handler(&store, &authorization, &publisher, &clock)
                .execute(&subject(), input(1.0, 2.0)),
            Err(CourierApplicationError::DependencyUnavailable(
                "valkey down".to_owned()
            ))
        );
    }

    /// AC-308
    #[test]
    fn out_of_range_coordinates_are_rejected_without_changes() {
        let store = seeded();
        let authorization = StaticAuthorization::allow_all();
        let publisher = RecordingPositionPublisher::new();
        let clock = ManualClock::new(1_000);
        let handler = handler(&store, &authorization, &publisher, &clock);
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
        let publisher = RecordingPositionPublisher::new();
        let clock = ManualClock::new(1_000);

        assert_eq!(
            handler(&store, &authorization, &publisher, &clock)
                .execute(&subject(), input(1.0, 2.0)),
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
        let publisher = RecordingPositionPublisher::new();
        let clock = ManualClock::new(1_000);

        assert_eq!(
            handler(&store, &authorization, &publisher, &clock)
                .execute(&subject(), input(1.0, 2.0)),
            Err(CourierApplicationError::PermissionDenied)
        );
        assert_eq!(store.latest(&CourierId::new("courier-1")), Ok(None));
    }
}

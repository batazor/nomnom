//! Set Courier Availability: a courier or a dispatcher of the Courier Pool
//! (FR-620) marks the courier available or unavailable for new assignments.
//! The last known location is not touched; a retired courier stays off line.

use crate::pkg::application::{AuthorizationError, Subject};
use crate::slices::courier::domains::courier::{
    CourierError, CourierId, CourierRepository, CourierStatus,
};

use super::authorization::{CourierAuthorization, CourierPermission, CourierResource};
use super::courier_id;
use super::error::CourierApplicationError;

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum Availability {
    Available,
    Unavailable,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct Input {
    pub courier_id: String,
    pub availability: Availability,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct Output {
    pub courier_id: CourierId,
    pub status: CourierStatus,
    /// `false` when the requested availability was already in effect.
    pub changed: bool,
}

pub struct Handler<'a> {
    pub authorization: &'a dyn CourierAuthorization,
    pub couriers: &'a dyn CourierRepository,
}

impl Handler<'_> {
    pub fn execute(
        &self,
        subject: &Subject,
        input: Input,
    ) -> Result<Output, CourierApplicationError> {
        let courier_id = courier_id(&input.courier_id)?;
        // The courier itself (`courier#act`), otherwise a dispatcher.
        match self.authorization.check(
            subject,
            CourierPermission::SetCourierAvailability,
            &CourierResource::Courier(courier_id.clone()),
        ) {
            Ok(()) => {}
            Err(AuthorizationError::Denied) => self.authorization.check(
                subject,
                CourierPermission::ManageCouriers,
                &CourierResource::Platform,
            )?,
            Err(error) => return Err(error.into()),
        }

        let mut courier = self
            .couriers
            .get(&courier_id)?
            .ok_or_else(|| CourierApplicationError::CourierNotFound(courier_id.clone()))?;
        let result = match input.availability {
            Availability::Available => courier.mark_available(),
            Availability::Unavailable => courier.mark_unavailable(),
        };
        let changed = match result {
            Ok(()) => true,
            Err(CourierError::AlreadyAvailable | CourierError::AlreadyUnavailable) => false,
            Err(error) => return Err(error.into()),
        };
        if changed {
            self.couriers.save(&courier)?;
        }
        Ok(Output {
            courier_id,
            status: courier.status(),
            changed,
        })
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::infrastructure::memory::StaticAuthorization;
    use crate::infrastructure::repository::test_support::TestStore;
    use crate::slices::courier::applications::update_courier_location::{
        CourierLocationStore, CourierPosition,
    };
    use crate::slices::courier::domains::courier::{Courier, CourierName, Location};

    fn courier_subject() -> Subject {
        Subject::User("courier-1".to_owned())
    }

    fn seeded(status: CourierStatus) -> TestStore {
        let store = TestStore::new();
        store
            .add(&Courier::restore(
                CourierId::new("courier-1"),
                CourierName::new("Timur").expect("courier name"),
                status,
            ))
            .expect("seed courier");
        store
    }

    fn input(availability: Availability) -> Input {
        Input {
            courier_id: "courier-1".to_owned(),
            availability,
        }
    }

    fn status(store: &TestStore) -> CourierStatus {
        store
            .get(&CourierId::new("courier-1"))
            .expect("read courier")
            .expect("courier exists")
            .status()
    }

    /// AC-304
    #[test]
    fn available_courier_goes_unavailable_without_touching_location() {
        let store = seeded(CourierStatus::Available);
        let position = CourierPosition {
            courier_id: CourierId::new("courier-1"),
            location: Location::new(52.52, 13.405).expect("location"),
            recorded_at_epoch_seconds: 100,
        };
        store.record(&position).expect("seed position");
        let authorization = StaticAuthorization::allow_all();
        let handler = Handler {
            authorization: &authorization,
            couriers: &store,
        };

        let output = handler
            .execute(&courier_subject(), input(Availability::Unavailable))
            .expect("availability changed");

        assert_eq!(output.status, CourierStatus::Unavailable);
        assert!(output.changed);
        assert_eq!(status(&store), CourierStatus::Unavailable);
        assert_eq!(
            store.latest(&CourierId::new("courier-1")),
            Ok(Some(position))
        );
        assert_eq!(
            authorization.courier_checks(),
            vec![(
                courier_subject(),
                CourierPermission::SetCourierAvailability,
                CourierResource::Courier(CourierId::new("courier-1"))
            )]
        );
    }

    #[test]
    fn unavailable_courier_returns_to_available() {
        let store = seeded(CourierStatus::Unavailable);
        let authorization = StaticAuthorization::allow_all();
        let handler = Handler {
            authorization: &authorization,
            couriers: &store,
        };

        let output = handler
            .execute(&courier_subject(), input(Availability::Available))
            .expect("availability changed");
        assert!(output.changed);
        assert_eq!(status(&store), CourierStatus::Available);
    }

    /// AC-305
    #[test]
    fn busy_courier_cannot_go_unavailable() {
        let store = seeded(CourierStatus::Busy);
        let authorization = StaticAuthorization::allow_all();
        let handler = Handler {
            authorization: &authorization,
            couriers: &store,
        };

        assert_eq!(
            handler.execute(&courier_subject(), input(Availability::Unavailable)),
            Err(CourierApplicationError::PreconditionFailed(
                CourierError::Busy
            ))
        );
        assert_eq!(status(&store), CourierStatus::Busy);
    }

    /// AC-306
    #[test]
    fn repeating_current_availability_is_a_no_op() {
        let store = seeded(CourierStatus::Available);
        let authorization = StaticAuthorization::allow_all();
        let handler = Handler {
            authorization: &authorization,
            couriers: &store,
        };

        let output = handler
            .execute(&courier_subject(), input(Availability::Available))
            .expect("no-op");
        assert_eq!(
            output,
            Output {
                courier_id: CourierId::new("courier-1"),
                status: CourierStatus::Available,
                changed: false,
            }
        );
    }

    /// AC-300
    #[test]
    fn denied_subject_does_not_change_availability() {
        let store = seeded(CourierStatus::Available);
        let authorization = StaticAuthorization::deny_all();
        let handler = Handler {
            authorization: &authorization,
            couriers: &store,
        };

        assert_eq!(
            handler.execute(
                &Subject::User("intruder".to_owned()),
                input(Availability::Unavailable)
            ),
            Err(CourierApplicationError::PermissionDenied)
        );
        assert_eq!(status(&store), CourierStatus::Available);
    }

    /// AC-790: a dispatcher of the Courier Pool puts another courier on line.
    #[test]
    fn dispatcher_changes_the_availability_of_any_courier() {
        let store = seeded(CourierStatus::Unavailable);
        let dispatcher = Subject::User("dispatcher".to_owned());
        let authorization = StaticAuthorization::grants_only();
        authorization.grant_courier(
            dispatcher.clone(),
            CourierPermission::ManageCouriers,
            CourierResource::Platform,
        );
        let handler = Handler {
            authorization: &authorization,
            couriers: &store,
        };

        let output = handler
            .execute(&dispatcher, input(Availability::Available))
            .expect("dispatcher puts the courier on line");

        assert_eq!(output.status, CourierStatus::Available);
        assert_eq!(status(&store), CourierStatus::Available);
        assert_eq!(
            authorization.courier_checks(),
            vec![
                (
                    dispatcher.clone(),
                    CourierPermission::SetCourierAvailability,
                    CourierResource::Courier(CourierId::new("courier-1"))
                ),
                (
                    dispatcher,
                    CourierPermission::ManageCouriers,
                    CourierResource::Platform
                ),
            ]
        );
    }

    /// FR-619: a retired courier does not return to the line.
    #[test]
    fn retired_courier_stays_off_line() {
        let store = TestStore::new();
        store
            .add(
                &Courier::restore(
                    CourierId::new("courier-1"),
                    CourierName::new("Timur").expect("courier name"),
                    CourierStatus::Unavailable,
                )
                .with_retired_at(Some(10)),
            )
            .expect("seed courier");
        let authorization = StaticAuthorization::allow_all();
        let handler = Handler {
            authorization: &authorization,
            couriers: &store,
        };

        assert_eq!(
            handler.execute(&courier_subject(), input(Availability::Available)),
            Err(CourierApplicationError::PreconditionFailed(
                CourierError::Retired
            ))
        );
        assert_eq!(status(&store), CourierStatus::Unavailable);
    }

    #[test]
    fn unknown_courier_is_not_found() {
        let store = TestStore::new();
        let authorization = StaticAuthorization::allow_all();
        let handler = Handler {
            authorization: &authorization,
            couriers: &store,
        };
        assert_eq!(
            handler.execute(&courier_subject(), input(Availability::Unavailable)),
            Err(CourierApplicationError::CourierNotFound(CourierId::new(
                "courier-1"
            )))
        );
    }
}

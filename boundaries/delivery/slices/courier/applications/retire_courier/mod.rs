//! Retire Courier: an operator or dispatcher takes a courier that is off line
//! out of the Courier Pool for good (FR-619). The courier stays in the history
//! of its deliveries; a courier on line stays with `OnLine`.

use crate::pkg::authz::Subject;
use crate::pkg::usecase::Clock;
use crate::slices::courier::domains::courier::{CourierError, CourierId, CourierRepository};

use super::authorization::{CourierAuthorization, CourierPermission, CourierResource};
use super::courier_id;
use super::error::CourierApplicationError;

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct Input {
    pub courier_id: String,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct Output {
    pub courier_id: CourierId,
    /// `false` when the courier had already retired.
    pub changed: bool,
}

pub struct Handler<'a> {
    pub authorization: &'a dyn CourierAuthorization,
    pub couriers: &'a dyn CourierRepository,
    pub clock: &'a dyn Clock,
}

impl Handler<'_> {
    pub fn execute(
        &self,
        subject: &Subject,
        input: Input,
    ) -> Result<Output, CourierApplicationError> {
        let courier_id = courier_id(&input.courier_id)?;
        self.authorization.check(
            subject,
            CourierPermission::ManageCouriers,
            &CourierResource::Platform,
        )?;

        let mut courier = self
            .couriers
            .get(&courier_id)?
            .ok_or_else(|| CourierApplicationError::CourierNotFound(courier_id.clone()))?;
        let changed = match courier.retire(self.clock.now_epoch_seconds()) {
            Ok(()) => true,
            Err(CourierError::AlreadyRetired) => false,
            Err(error) => return Err(error.into()),
        };
        if changed {
            self.couriers.save(&courier)?;
        }
        Ok(Output {
            courier_id,
            changed,
        })
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::infrastructure::memory::{ManualClock, StaticAuthorization};
    use crate::infrastructure::repository::test_support::TestStore;
    use crate::slices::courier::domains::courier::MarketId;
    use crate::slices::courier::domains::courier::{Courier, CourierName, CourierStatus};

    fn dispatcher() -> Subject {
        Subject::User("dispatcher".to_owned())
    }

    fn seeded(status: CourierStatus) -> TestStore {
        let store = TestStore::new();
        store
            .add(&Courier::restore(
                CourierId::new("courier-1"),
                CourierName::new("Anna").expect("courier name"),
                MarketId::new("berlin").expect("market id"),
                status,
            ))
            .expect("seed courier");
        store
    }

    fn input() -> Input {
        Input {
            courier_id: "courier-1".to_owned(),
        }
    }

    fn stored(store: &TestStore) -> Courier {
        store
            .get(&CourierId::new("courier-1"))
            .expect("read courier")
            .expect("courier exists")
    }

    /// AC-788
    #[test]
    fn courier_off_line_retires_once() {
        let store = seeded(CourierStatus::Unavailable);
        let authorization = StaticAuthorization::allow_all();
        let clock = ManualClock::new(1_000);
        let handler = Handler {
            authorization: &authorization,
            couriers: &store,
            clock: &clock,
        };

        let first = handler.execute(&dispatcher(), input()).expect("retired");
        let repeat = handler.execute(&dispatcher(), input()).expect("repeat");

        assert!(first.changed);
        assert!(!repeat.changed);
        assert_eq!(stored(&store).retired_at_epoch_seconds(), Some(1_000));
        assert_eq!(
            authorization.courier_checks()[0],
            (
                dispatcher(),
                CourierPermission::ManageCouriers,
                CourierResource::Platform
            )
        );
    }

    /// AC-789
    #[test]
    fn courier_on_line_stays_in_the_pool() {
        for status in [CourierStatus::Available, CourierStatus::Busy] {
            let store = seeded(status);
            let authorization = StaticAuthorization::allow_all();
            let clock = ManualClock::new(1_000);
            let handler = Handler {
                authorization: &authorization,
                couriers: &store,
                clock: &clock,
            };

            assert_eq!(
                handler.execute(&dispatcher(), input()),
                Err(CourierApplicationError::PreconditionFailed(
                    CourierError::OnLine
                ))
            );
            assert!(!stored(&store).is_retired());
        }
    }

    /// AC-791
    #[test]
    fn subject_without_the_pool_right_retires_nobody() {
        let store = seeded(CourierStatus::Unavailable);
        let authorization = StaticAuthorization::deny_all();
        let clock = ManualClock::new(1_000);
        let handler = Handler {
            authorization: &authorization,
            couriers: &store,
            clock: &clock,
        };

        assert_eq!(
            handler.execute(&dispatcher(), input()),
            Err(CourierApplicationError::PermissionDenied)
        );
        assert!(!stored(&store).is_retired());
    }
}

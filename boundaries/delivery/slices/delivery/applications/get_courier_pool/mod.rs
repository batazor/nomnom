//! Get Courier Pool: an operator or dispatcher reads the Pool Load and every
//! courier that is not retired with its pending offer and active Delivery
//! (FR-621). The query reads couriers, offers and deliveries, so it belongs to
//! the `delivery` slice; the right is the `courier` slice's `ManageCouriers`.

use crate::pkg::application::{Clock, Subject};
use crate::pkg::persistence::RepositoryError;
use crate::slices::courier::applications::authorization::{
    CourierAuthorization, CourierPermission, CourierResource,
};
use crate::slices::courier::domains::courier::{CourierId, CourierName, CourierStatus};
use crate::slices::delivery::domains::delivery::{DeliveryId, DeliveryOfferId};

use super::error::DeliveryApplicationError;

/// One courier of the pool.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct PoolCourierView {
    pub courier_id: CourierId,
    pub name: CourierName,
    pub status: CourierStatus,
    /// Pending, not yet expired Delivery Offer addressed to the courier.
    pub pending_offer_id: Option<DeliveryOfferId>,
    /// Assigned or picked up Delivery of the courier.
    pub active_delivery_id: Option<DeliveryId>,
}

/// Query model of the Courier Pool.
pub trait CourierPoolReadModel {
    /// Couriers that are not retired, by name, then by id; offers pending at
    /// `now_epoch_seconds`.
    fn pool_couriers(
        &self,
        now_epoch_seconds: i64,
    ) -> Result<Vec<PoolCourierView>, RepositoryError>;

    /// Deliveries in `Waiting`: the backlog of the pool.
    fn waiting_deliveries(&self) -> Result<u64, RepositoryError>;
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct Output {
    pub size: u64,
    /// Available and busy couriers.
    pub on_line: u64,
    pub busy: u64,
    pub waiting: u64,
    pub couriers: Vec<PoolCourierView>,
}

pub struct Handler<'a> {
    pub authorization: &'a dyn CourierAuthorization,
    pub pool: &'a dyn CourierPoolReadModel,
    pub clock: &'a dyn Clock,
}

impl Handler<'_> {
    pub fn execute(&self, subject: &Subject) -> Result<Output, DeliveryApplicationError> {
        self.authorization.check(
            subject,
            CourierPermission::ManageCouriers,
            &CourierResource::Platform,
        )?;

        let couriers = self.pool.pool_couriers(self.clock.now_epoch_seconds())?;
        let waiting = self.pool.waiting_deliveries()?;
        let count = |status: &[CourierStatus]| {
            couriers
                .iter()
                .filter(|courier| status.contains(&courier.status))
                .count() as u64
        };
        Ok(Output {
            size: couriers.len() as u64,
            on_line: count(&[CourierStatus::Available, CourierStatus::Busy]),
            busy: count(&[CourierStatus::Busy]),
            waiting,
            couriers,
        })
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::infrastructure::memory::StaticAuthorization;
    use crate::slices::courier::domains::courier::{Courier, CourierRepository};
    use crate::slices::delivery::applications::test_support::{OFFER_TTL, World};

    fn dispatcher() -> Subject {
        Subject::User("dispatcher".to_owned())
    }

    fn handler(world: &World) -> Handler<'_> {
        Handler {
            authorization: &world.authorization,
            pool: &world.store,
            clock: &world.clock,
        }
    }

    /// AC-792
    #[test]
    fn pool_load_counts_the_line_the_busy_couriers_and_the_backlog() {
        let world = World::new();
        world.courier("c-available", CourierStatus::Available);
        world.waiting_delivery("d-offered");
        world.offer("o-1", "d-offered", "c-available");
        world.waiting_delivery("d-waiting");
        world.assigned_delivery("d-assigned", "c-busy");
        world.courier("c-off", CourierStatus::Unavailable);
        CourierRepository::add(
            &world.store,
            &Courier::restore(
                CourierId::new("c-retired"),
                CourierName::new("Anna").expect("courier name"),
                CourierStatus::Unavailable,
            )
            .with_retired_at(Some(10)),
        )
        .expect("seed retired courier");

        let pool = handler(&world).execute(&dispatcher()).expect("pool");

        assert_eq!(
            (pool.size, pool.on_line, pool.busy, pool.waiting),
            (3, 2, 1, 2)
        );
        let by_id = |id: &str| {
            pool.couriers
                .iter()
                .find(|courier| courier.courier_id.as_str() == id)
                .expect("courier in the pool")
        };
        assert_eq!(
            by_id("c-available").pending_offer_id,
            Some(DeliveryOfferId::new("o-1"))
        );
        assert_eq!(
            by_id("c-busy").active_delivery_id,
            Some(DeliveryId::new("d-assigned"))
        );
        assert_eq!(by_id("c-off").pending_offer_id, None);
        assert!(
            pool.couriers
                .iter()
                .all(|courier| courier.courier_id.as_str() != "c-retired")
        );
    }

    #[test]
    fn an_expired_offer_is_not_pending() {
        let world = World::new();
        world.courier("c-available", CourierStatus::Available);
        world.waiting_delivery("d-offered");
        world.offer("o-1", "d-offered", "c-available");
        world.clock.advance(OFFER_TTL);

        let pool = handler(&world).execute(&dispatcher()).expect("pool");

        assert_eq!(pool.couriers[0].pending_offer_id, None);
    }

    /// AC-791
    #[test]
    fn subject_without_the_pool_right_reads_nothing() {
        let world = World::with_authorization(StaticAuthorization::deny_all());

        assert_eq!(
            handler(&world).execute(&dispatcher()),
            Err(DeliveryApplicationError::PermissionDenied)
        );
        assert_eq!(
            world.authorization.courier_checks(),
            vec![(
                dispatcher(),
                CourierPermission::ManageCouriers,
                CourierResource::Platform
            )]
        );
    }
}

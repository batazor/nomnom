//! Get Courier Pool: an operator or dispatcher reads the Pool Load and every
//! courier that is not retired with its pending offer and active Delivery
//! (FR-621) of one Market (ADR 41): couriers, load counts and the backlog of
//! waiting deliveries are those of the requested Market only. The query reads couriers, offers and deliveries, so it belongs to
//! the `delivery` slice; the right is the `courier` slice's `ManageCouriers`.

use crate::pkg::application::{Clock, Subject};
use crate::pkg::persistence::RepositoryError;
use crate::slices::courier::applications::authorization::{
    CourierAuthorization, CourierPermission, CourierResource,
};
use crate::slices::courier::domains::courier::{CourierId, CourierName, CourierStatus, MarketId};
use crate::slices::delivery::domains::delivery::{DeliveryId, DeliveryOfferId, OrderId};

use super::error::DeliveryApplicationError;
use super::market_id;

/// One courier of the pool.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct PoolCourierView {
    pub courier_id: CourierId,
    pub name: CourierName,
    pub status: CourierStatus,
    /// Pending, not yet expired Delivery Offer addressed to the courier.
    pub pending_offer_id: Option<DeliveryOfferId>,
    /// Order of the pending offer.
    pub offered_order_id: Option<OrderId>,
    /// Assigned or picked up Delivery of the courier.
    pub active_delivery_id: Option<DeliveryId>,
    /// Order of the active Delivery: the flow board names the courier on it.
    pub active_order_id: Option<OrderId>,
}

/// Query model of the Courier Pool.
pub trait CourierPoolReadModel {
    /// Couriers of `market_id` that are not retired, by name, then by id;
    /// offers pending at `now_epoch_seconds`.
    fn pool_couriers(
        &self,
        market_id: &MarketId,
        now_epoch_seconds: i64,
    ) -> Result<Vec<PoolCourierView>, RepositoryError>;

    /// Deliveries of `market_id` in `Waiting`: the backlog of the pool.
    fn waiting_deliveries(&self, market_id: &MarketId) -> Result<u64, RepositoryError>;
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct Input {
    /// Market of the Courier Pool; required.
    pub market_id: String,
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
    pub fn execute(
        &self,
        subject: &Subject,
        input: Input,
    ) -> Result<Output, DeliveryApplicationError> {
        let market_id = market_id(&input.market_id)?;
        self.authorization.check(
            subject,
            CourierPermission::ManageCouriers,
            &CourierResource::Platform,
        )?;

        let couriers = self
            .pool
            .pool_couriers(&market_id, self.clock.now_epoch_seconds())?;
        let waiting = self.pool.waiting_deliveries(&market_id)?;
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
    use crate::slices::courier::domains::courier::{Courier, CourierRepository, MarketIdError};
    use crate::slices::delivery::applications::error::InvalidDeliveryRequest;
    use crate::slices::delivery::applications::test_support::{OFFER_TTL, World, berlin, hamburg};

    fn berlin_pool() -> Input {
        Input {
            market_id: "berlin".to_owned(),
        }
    }

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
                berlin(),
                CourierStatus::Unavailable,
            )
            .with_retired_at(Some(10)),
        )
        .expect("seed retired courier");
        // ADR 41: couriers and the backlog of another Market are not counted.
        world.courier_in("c-hamburg", CourierStatus::Available, &hamburg());
        world.waiting_delivery_in("d-hamburg", &hamburg());

        let pool = handler(&world)
            .execute(&dispatcher(), berlin_pool())
            .expect("pool");

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
        assert!(by_id("c-busy").active_order_id.is_some());
        assert!(by_id("c-available").offered_order_id.is_some());
        assert_eq!(by_id("c-off").pending_offer_id, None);
        assert!(
            pool.couriers
                .iter()
                .all(|courier| !["c-retired", "c-hamburg"].contains(&courier.courier_id.as_str()))
        );

        let hamburg_pool = handler(&world)
            .execute(
                &dispatcher(),
                Input {
                    market_id: "hamburg".to_owned(),
                },
            )
            .expect("hamburg pool");
        assert_eq!(
            (
                hamburg_pool.size,
                hamburg_pool.on_line,
                hamburg_pool.busy,
                hamburg_pool.waiting
            ),
            (1, 1, 0, 1)
        );
        assert_eq!(hamburg_pool.couriers[0].courier_id.as_str(), "c-hamburg");
    }

    /// ADR 41: the Market of the pool is required.
    #[test]
    fn pool_without_a_valid_market_is_invalid() {
        let world = World::new();
        for (market, error) in [
            ("", MarketIdError::Empty),
            ("Berlin", MarketIdError::InvalidFormat),
        ] {
            assert_eq!(
                handler(&world).execute(
                    &dispatcher(),
                    Input {
                        market_id: market.to_owned(),
                    }
                ),
                Err(DeliveryApplicationError::InvalidRequest(
                    InvalidDeliveryRequest::Market(error)
                ))
            );
        }
        assert!(world.authorization.courier_checks().is_empty());
    }

    #[test]
    fn an_expired_offer_is_not_pending() {
        let world = World::new();
        world.courier("c-available", CourierStatus::Available);
        world.waiting_delivery("d-offered");
        world.offer("o-1", "d-offered", "c-available");
        world.clock.advance(OFFER_TTL);

        let pool = handler(&world)
            .execute(&dispatcher(), berlin_pool())
            .expect("pool");

        assert_eq!(pool.couriers[0].pending_offer_id, None);
    }

    /// AC-791
    #[test]
    fn subject_without_the_pool_right_reads_nothing() {
        let world = World::with_authorization(StaticAuthorization::deny_all());

        assert_eq!(
            handler(&world).execute(&dispatcher(), berlin_pool()),
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

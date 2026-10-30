//! Create Delivery: the fulfillment process of `orders` requests delivery of
//! an order in the Market of the order (ADR 41). Idempotent by `OrderId`: a
//! repeat with the same Market replays the Delivery, a repeat with another
//! Market is rejected — the Market of a Delivery never changes. After commit,
//! and on every replay, it starts the Assignment Workflow of the Delivery,
//! which searches the courier (FR-306, FR-707). A new Delivery gets random pickup and dropoff points
//! of the Virtual City and a Tracking Channel (FR-659); a replay keeps the
//! stored ones. The new Delivery and its `DeliveryCreated` fact are written
//! in one Unit of Work; a replay writes nothing.

use crate::pkg::authz::Subject;
use crate::pkg::persistence::RepositoryError;
use crate::pkg::unit_of_work::{UnitOfWork, UnitOfWorkError};
use crate::pkg::usecase::{Clock, IdGenerator};
use crate::slices::courier::domains::courier::MarketId;
use crate::slices::delivery::domains::delivery::{
    Delivery, DeliveryId, DeliveryRepository, OrderId, VirtualCity,
};

use super::assignment_process::AssignmentProcess;
use super::authorization::{DeliveryAuthorization, DeliveryPermission, DeliveryResource};
use super::error::DeliveryApplicationError;
use super::outbox::{DeliveryOutbox, publish};
use super::tracking::{RandomSource, new_tracking};
use super::{market_id, order_id};

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct Input {
    pub order_id: String,
    /// Market of the order; required.
    pub market_id: String,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct Output {
    pub delivery_id: DeliveryId,
    /// `false` when an earlier request for the order already created it.
    pub created: bool,
}

pub struct Handler<'a, U: UnitOfWork> {
    pub authorization: &'a dyn DeliveryAuthorization,
    pub unit_of_work: &'a U,
    pub deliveries: &'a dyn DeliveryRepository,
    pub outbox: &'a dyn DeliveryOutbox,
    pub ids: &'a dyn IdGenerator,
    pub clock: &'a dyn Clock,
    pub city: &'a VirtualCity,
    pub random: &'a dyn RandomSource,
    pub assignment: &'a dyn AssignmentProcess,
}

/// Failure of the insert transaction; a uniqueness conflict is a lost race
/// against a concurrent request for the order, resolved after the rollback.
enum InsertError {
    Conflict,
    Failed(DeliveryApplicationError),
}

impl From<UnitOfWorkError> for InsertError {
    fn from(error: UnitOfWorkError) -> Self {
        Self::Failed(error.into())
    }
}

impl From<RepositoryError> for InsertError {
    fn from(error: RepositoryError) -> Self {
        match error {
            RepositoryError::Conflict => Self::Conflict,
            other => Self::Failed(other.into()),
        }
    }
}

impl<U: UnitOfWork> Handler<'_, U> {
    pub fn execute(
        &self,
        subject: &Subject,
        input: Input,
    ) -> Result<Output, DeliveryApplicationError> {
        let output = self.create(subject, input)?;
        // A replay starts it again: the start after the first commit may have
        // been lost; a running workflow is left as it is.
        self.assignment.start(&output.delivery_id)?;
        Ok(output)
    }

    fn create(&self, subject: &Subject, input: Input) -> Result<Output, DeliveryApplicationError> {
        let order_id = order_id(&input.order_id)?;
        let market_id = market_id(&input.market_id)?;
        self.authorization.check(
            subject,
            DeliveryPermission::CreateDelivery,
            &DeliveryResource::Platform,
        )?;

        if let Some(existing) = self.deliveries.find_by_order(&order_id)? {
            return replay(&existing, &market_id);
        }
        let tracking = new_tracking(self.city, self.random)
            .map_err(|error| DeliveryApplicationError::DependencyUnavailable(error.to_string()))?;
        let mut delivery = Delivery::create(
            DeliveryId::new(self.ids.next_id()),
            order_id.clone(),
            market_id.clone(),
            self.clock.now_epoch_seconds(),
        )
        .with_tracking(Some(tracking));
        let delivery_id = delivery.id().clone();
        let inserted = self.unit_of_work.run(|| {
            self.deliveries.add(&delivery)?;
            publish(self.outbox, delivery.pull_events())?;
            Ok::<(), InsertError>(())
        });
        match inserted {
            Ok(()) => Ok(Output {
                delivery_id,
                created: true,
            }),
            Err(InsertError::Conflict) => {
                let existing = self.concurrent_delivery(&order_id)?;
                replay(&existing, &market_id)
            }
            Err(InsertError::Failed(error)) => Err(error),
        }
    }

    /// Resolves a lost insert race against a concurrent request for the order.
    fn concurrent_delivery(
        &self,
        order_id: &OrderId,
    ) -> Result<Delivery, DeliveryApplicationError> {
        self.deliveries.find_by_order(order_id)?.ok_or_else(|| {
            DeliveryApplicationError::Internal(
                "delivery insert conflicted without an existing delivery".to_owned(),
            )
        })
    }
}

/// A repeat for the same order replays the existing Delivery when the Market
/// is the same; another Market is a conflicting repeat.
fn replay(existing: &Delivery, market_id: &MarketId) -> Result<Output, DeliveryApplicationError> {
    if existing.market_id() != market_id {
        return Err(DeliveryApplicationError::DeliveryForOrderInOtherMarket {
            order_id: existing.order_id().clone(),
            market_id: existing.market_id().clone(),
        });
    }
    Ok(Output {
        delivery_id: existing.id().clone(),
        created: false,
    })
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::infrastructure::memory::{AssignmentCall, StaticAuthorization};
    use crate::infrastructure::repository::test_support::TestStore;
    use crate::slices::courier::domains::courier::MarketIdError;
    use crate::slices::delivery::applications::error::InvalidDeliveryRequest;
    use crate::slices::delivery::applications::outbox::OutboxMessage;
    use crate::slices::delivery::applications::test_support::{
        NOW, World, berlin, service, tracking,
    };
    use crate::slices::delivery::domains::delivery::{DomainEvent, State};

    fn input() -> Input {
        in_market("berlin")
    }

    fn in_market(market: &str) -> Input {
        Input {
            order_id: "order-1".to_owned(),
            market_id: market.to_owned(),
        }
    }

    fn handler(world: &World) -> Handler<'_, TestStore> {
        Handler {
            authorization: &world.authorization,
            unit_of_work: &world.store,
            deliveries: &world.store,
            outbox: &world.store,
            ids: &world.ids,
            clock: &world.clock,
            city: &world.city,
            random: &world.random,
            assignment: &world.assignment,
        }
    }

    /// AC-313
    #[test]
    fn creates_waiting_delivery_without_courier() {
        let world = World::new();

        let output = handler(&world)
            .execute(&service(), input())
            .expect("delivery created");

        assert_eq!(
            output,
            Output {
                delivery_id: DeliveryId::new("generated-1"),
                created: true,
            }
        );
        let delivery = world.delivery("generated-1");
        assert_eq!(delivery.state(), State::Waiting);
        assert_eq!(delivery.courier_id(), None);
        assert_eq!(delivery.order_id(), &OrderId::new("order-1"));
        assert_eq!(delivery.market_id(), &berlin());
        assert_eq!(delivery.created_at_epoch_seconds(), Some(NOW));
        // AC-872
        assert_eq!(delivery.tracking(), Some(&tracking()));
        assert_eq!(
            world.store.outbox_messages(),
            vec![OutboxMessage::delivery(DomainEvent::DeliveryCreated {
                delivery_id: DeliveryId::new("generated-1"),
                order_id: OrderId::new("order-1"),
                market_id: berlin(),
                created_at_epoch_seconds: NOW,
            })]
        );
        assert_eq!(world.store.commits(), 1);
    }

    /// AC-314
    #[test]
    fn identical_repeat_returns_existing_delivery() {
        let world = World::new();
        handler(&world).execute(&service(), input()).expect("first");

        let repeat = handler(&world)
            .execute(&service(), input())
            .expect("repeat");

        assert_eq!(
            repeat,
            Output {
                delivery_id: DeliveryId::new("generated-1"),
                created: false,
            }
        );
        assert_eq!(
            DeliveryRepository::find_by_order(&world.store, &OrderId::new("order-1"))
                .expect("read")
                .map(|delivery| delivery.id().clone()),
            Some(DeliveryId::new("generated-1"))
        );
        // The replay records no second `DeliveryCreated`; AC-873: the points
        // and the channel stay.
        assert_eq!(world.store.outbox_messages().len(), 1);
        assert_eq!(world.delivery("generated-1").tracking(), Some(&tracking()));
    }

    /// ADR 41: the Market of a Delivery is immutable, so a repeat for the
    /// order with another Market is a conflict, not a replay.
    #[test]
    fn repeat_with_another_market_is_rejected() {
        let world = World::new();
        handler(&world).execute(&service(), input()).expect("first");

        assert_eq!(
            handler(&world).execute(&service(), in_market("hamburg")),
            Err(DeliveryApplicationError::DeliveryForOrderInOtherMarket {
                order_id: OrderId::new("order-1"),
                market_id: berlin(),
            })
        );
        assert_eq!(world.delivery("generated-1").market_id(), &berlin());
        assert_eq!(world.store.outbox_messages().len(), 1);
    }

    /// ADR 41: `market_id` is required and a slug.
    #[test]
    fn missing_or_malformed_market_is_invalid() {
        let world = World::new();
        for (market, error) in [
            ("", MarketIdError::Empty),
            ("Berlin", MarketIdError::InvalidFormat),
        ] {
            assert_eq!(
                handler(&world).execute(&service(), in_market(market)),
                Err(DeliveryApplicationError::InvalidRequest(
                    InvalidDeliveryRequest::Market(error)
                ))
            );
        }
        assert_eq!(
            DeliveryRepository::find_by_order(&world.store, &OrderId::new("order-1")),
            Ok(None)
        );
    }

    #[test]
    fn denied_subject_creates_nothing() {
        let world = World::with_authorization(StaticAuthorization::deny_all());
        assert_eq!(
            handler(&world).execute(&service(), input()),
            Err(DeliveryApplicationError::PermissionDenied)
        );
        assert_eq!(
            DeliveryRepository::find_by_order(&world.store, &OrderId::new("order-1")),
            Ok(None)
        );
    }

    /// AC-958: every request starts the Assignment Workflow after commit,
    /// the replay too: the start after the first commit may have been lost.
    #[test]
    fn create_and_replay_start_the_assignment_workflow() {
        let world = World::new();
        world.assignment.set_failure(Some("temporal down"));

        assert_eq!(
            handler(&world).execute(&service(), input()),
            Err(DeliveryApplicationError::DependencyUnavailable(
                "assignment workflow unavailable: temporal down".to_owned()
            ))
        );
        assert_eq!(world.store.commits(), 1, "the delivery stays created");

        world.assignment.set_failure(None);
        let repeat = handler(&world)
            .execute(&service(), input())
            .expect("replay");

        assert!(!repeat.created);
        assert_eq!(
            world.assignment.calls(),
            vec![AssignmentCall::Start(DeliveryId::new("generated-1"))]
        );
    }
}

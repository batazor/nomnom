use super::*;
use crate::infrastructure::memory::MemoryStore;
use crate::pkg::persistence::RepositoryError;
use crate::slices::courier::domains::courier::{CourierId, CourierStatus};
use crate::slices::delivery::applications::outbox::OutboxMessage;
use crate::slices::delivery::applications::test_support::{
    NOW, OFFER_TTL, World, candidate, new_delivery,
};
use crate::slices::delivery::domains::delivery::{
    DeliveryOfferId, DomainEvent, OfferState, OrderId, State,
};

fn handler(world: &World) -> Handler<'_, MemoryStore> {
    Handler {
        unit_of_work: &world.store,
        deliveries: &world.store,
        couriers: &world.store,
        offers: &world.store,
        outbox: &world.store,
        locator: &world.locator,
        ids: &world.ids,
        clock: &world.clock,
        policy: world.policy,
    }
}

const BATCH: Input = Input {
    batch_size: 100,
    after: None,
};

/// Waiting delivery created at `created_at`.
fn created(world: &World, id: &str, created_at: i64) {
    DeliveryRepository::add(
        &world.store,
        &new_delivery(id).with_created_at(Some(created_at)),
    )
    .expect("seed delivery");
}

fn on_line(world: &World, couriers: &[&str]) {
    for courier in couriers {
        world.courier(courier, CourierStatus::Available);
    }
    world.locator.set_candidates(
        couriers
            .iter()
            .map(|courier| candidate(courier, 500))
            .collect(),
    );
}

fn offered_fact(delivery: &str, offer: &str, courier: &str, expires_at: i64) -> OutboxMessage {
    OutboxMessage::delivery(DomainEvent::DeliveryOffered {
        delivery_id: DeliveryId::new(delivery),
        offer_id: DeliveryOfferId::new(offer),
        courier_id: CourierId::new(courier),
        expires_at_epoch_seconds: expires_at,
    })
}

fn offered_couriers(output: &Output) -> Vec<(&str, &str)> {
    output
        .offered
        .iter()
        .map(|offer| (offer.delivery_id.as_str(), offer.courier_id.as_str()))
        .collect()
}

fn ids(values: &[&str]) -> Vec<DeliveryId> {
    values.iter().copied().map(DeliveryId::new).collect()
}

/// AC-734
#[test]
fn created_delivery_is_offered_on_the_next_pass() {
    let world = World::new();
    on_line(&world, &["courier-1"]);
    created(&world, "delivery-1", NOW);

    let output = handler(&world).execute(BATCH).expect("dispatched");

    assert_eq!(offered_couriers(&output), vec![("delivery-1", "courier-1")]);
    assert!(output.without_candidate.is_empty() && output.skipped.is_empty());
    assert_eq!(output.next, None);
    assert_eq!(world.offer_state("generated-1"), OfferState::Pending);
    assert_eq!(world.delivery_state("delivery-1"), State::Waiting);
    assert_eq!(
        world.store.outbox_messages(),
        vec![offered_fact(
            "delivery-1",
            "generated-1",
            "courier-1",
            NOW + OFFER_TTL
        )]
    );
    assert_eq!(world.store.commits(), 1);

    // A pending offer takes the delivery out of the next pass.
    assert_eq!(handler(&world).execute(BATCH), Ok(Output::default()));
}

/// AC-735
#[test]
fn rejected_delivery_is_offered_to_another_courier() {
    let world = World::new();
    on_line(&world, &["courier-1", "courier-2"]);
    created(&world, "delivery-1", NOW);
    handler(&world).execute(BATCH).expect("first offer");
    let mut first = world.store_offer("generated-1");
    assert_eq!(first.courier_id(), &CourierId::new("courier-1"));
    first
        .reject(&CourierId::new("courier-1"), NOW)
        .expect("rejected");
    DeliveryOfferRepository::save(&world.store, &first).expect("save");

    let output = handler(&world).execute(BATCH).expect("dispatched again");

    assert_eq!(offered_couriers(&output), vec![("delivery-1", "courier-2")]);
}

/// AC-735
#[test]
fn expired_delivery_is_offered_to_another_courier() {
    let world = World::new();
    on_line(&world, &["courier-1", "courier-2"]);
    created(&world, "delivery-1", NOW);
    world.offer("offer-1", "delivery-1", "courier-1");
    world.clock.set(NOW + OFFER_TTL + 1);
    let mut expired = world.store_offer("offer-1");
    expired.expire(NOW + OFFER_TTL + 1).expect("expired");
    DeliveryOfferRepository::save(&world.store, &expired).expect("save");

    let output = handler(&world).execute(BATCH).expect("dispatched");

    assert_eq!(offered_couriers(&output), vec![("delivery-1", "courier-2")]);
    // Everyone was offered it once: the delivery waits for Replan.
    let mut second = world.store_offer("generated-1");
    second
        .reject(&CourierId::new("courier-2"), NOW + OFFER_TTL + 1)
        .expect("rejected");
    DeliveryOfferRepository::save(&world.store, &second).expect("save");
    let output = handler(&world).execute(BATCH).expect("no one left");
    assert_eq!(output.without_candidate, ids(&["delivery-1"]));
}

/// AC-736
#[test]
fn delivery_waits_until_a_courier_goes_on_line() {
    let world = World::new();
    created(&world, "delivery-1", NOW);
    world.courier("courier-1", CourierStatus::Unavailable);
    world
        .locator
        .set_candidates(vec![candidate("courier-1", 500)]);

    let output = handler(&world).execute(BATCH).expect("no error");

    assert!(output.offered.is_empty());
    assert_eq!(output.without_candidate, ids(&["delivery-1"]));
    assert_eq!(world.delivery_state("delivery-1"), State::Waiting);
    assert!(world.store.outbox_messages().is_empty());
    assert!(
        DeliveryOfferRepository::list_for_delivery(&world.store, &DeliveryId::new("delivery-1"))
            .expect("read")
            .is_empty()
    );

    world.courier("courier-2", CourierStatus::Available);
    world.locator.set_candidates(vec![
        candidate("courier-1", 500),
        candidate("courier-2", 500),
    ]);
    let output = handler(&world).execute(BATCH).expect("courier on line");
    assert_eq!(offered_couriers(&output), vec![("delivery-1", "courier-2")]);
}

#[test]
fn batches_are_oldest_first_and_page_by_position() {
    let world = World::new();
    created(&world, "delivery-b", NOW + 2);
    created(&world, "delivery-c", NOW + 1);
    created(&world, "delivery-a", NOW + 2);
    // Stored before creation time was recorded: counts as the oldest.
    DeliveryRepository::add(&world.store, &new_delivery("delivery-old")).expect("seed");
    world.assigned_delivery("delivery-assigned", "busy");

    let first = handler(&world)
        .execute(Input {
            batch_size: 2,
            after: None,
        })
        .expect("first batch");
    assert_eq!(
        first.without_candidate,
        ids(&["delivery-old", "delivery-c"])
    );
    let next = first.next.expect("full batch");
    assert_eq!(next.delivery_id, DeliveryId::new("delivery-c"));

    let second = handler(&world)
        .execute(Input {
            batch_size: 2,
            after: Some(next),
        })
        .expect("second batch");
    assert_eq!(second.without_candidate, ids(&["delivery-a", "delivery-b"]));
    let third = handler(&world)
        .execute(Input {
            batch_size: 2,
            after: second.next,
        })
        .expect("third batch");
    assert_eq!(third, Output::default());
    assert_eq!(
        handler(&world).execute(Input {
            batch_size: 0,
            after: None,
        }),
        Ok(Output::default())
    );
}

/// Deliveries repository whose batch is read before a concurrent writer
/// changes the first delivery, so the dispatch claim hits a version
/// conflict.
struct ConcurrentChange<'a> {
    store: &'a MemoryStore,
}

impl DeliveryRepository for ConcurrentChange<'_> {
    fn get(&self, id: &DeliveryId) -> Result<Option<Delivery>, RepositoryError> {
        DeliveryRepository::get(self.store, id)
    }

    fn find_by_order(&self, order_id: &OrderId) -> Result<Option<Delivery>, RepositoryError> {
        self.store.find_by_order(order_id)
    }

    fn add(&self, delivery: &Delivery) -> Result<(), RepositoryError> {
        DeliveryRepository::add(self.store, delivery)
    }

    fn save(&self, delivery: &Delivery) -> Result<(), RepositoryError> {
        DeliveryRepository::save(self.store, delivery)
    }

    fn list_waiting_without_pending_offer(
        &self,
        after: Option<&DeliveryPosition>,
        limit: usize,
    ) -> Result<Vec<Delivery>, RepositoryError> {
        let batch = self
            .store
            .list_waiting_without_pending_offer(after, limit)?;
        // Another dispatcher claimed the first delivery after this read.
        DeliveryRepository::save(self.store, &batch[0])?;
        Ok(batch)
    }
}

#[test]
fn concurrently_changed_delivery_is_skipped() {
    let world = World::new();
    on_line(&world, &["courier-1"]);
    created(&world, "delivery-1", NOW);
    created(&world, "delivery-2", NOW + 1);
    let deliveries = ConcurrentChange {
        store: &world.store,
    };

    let output = Handler {
        deliveries: &deliveries,
        ..handler(&world)
    }
    .execute(BATCH)
    .expect("conflict is not an error");

    assert_eq!(output.skipped, ids(&["delivery-1"]));
    assert_eq!(offered_couriers(&output), vec![("delivery-2", "courier-1")]);
    assert!(
        DeliveryOfferRepository::list_for_delivery(&world.store, &DeliveryId::new("delivery-1"))
            .expect("read")
            .is_empty()
    );
    assert_eq!(world.store.rollbacks(), 1);
}

#[test]
fn locator_outage_is_an_error() {
    let world = World::new();
    created(&world, "delivery-1", NOW);
    world.locator.set_unavailable(true);

    assert!(matches!(
        handler(&world).execute(BATCH),
        Err(DeliveryApplicationError::DependencyUnavailable(_))
    ));
    assert_eq!(world.delivery_state("delivery-1"), State::Waiting);
}

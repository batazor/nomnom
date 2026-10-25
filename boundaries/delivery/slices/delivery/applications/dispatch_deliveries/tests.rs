use std::sync::{Mutex, PoisonError};

use super::*;
use crate::infrastructure::postgres::test_support::TestStore;
use crate::slices::courier::domains::courier::{CourierId, CourierStatus};
use crate::slices::delivery::applications::outbox::OutboxMessage;
use crate::slices::delivery::applications::test_support::{
    NOW, OFFER_TTL, World, candidate, new_delivery,
};
use crate::slices::delivery::domains::delivery::{DeliveryOfferId, DomainEvent, OfferState, State};

/// Scripted Waiting Deliveries query: answers with the deliveries the test
/// gives, in the given order, after the cursor and at most `limit`, and
/// records every call. Filtering and ordering in SQL are covered by the
/// PostgreSQL tests.
#[derive(Default)]
struct ScriptedWaiting {
    deliveries: Mutex<Vec<Delivery>>,
    calls: Mutex<Vec<(Option<DeliveryPosition>, usize)>>,
}

impl ScriptedWaiting {
    fn answer(&self, deliveries: Vec<Delivery>) {
        *self
            .deliveries
            .lock()
            .unwrap_or_else(PoisonError::into_inner) = deliveries;
    }

    fn calls(&self) -> Vec<(Option<DeliveryPosition>, usize)> {
        self.calls
            .lock()
            .unwrap_or_else(PoisonError::into_inner)
            .clone()
    }
}

impl WaitingDeliveries for ScriptedWaiting {
    fn list_waiting_without_pending_offer(
        &self,
        after: Option<&DeliveryPosition>,
        limit: usize,
    ) -> Result<Vec<Delivery>, RepositoryError> {
        self.calls
            .lock()
            .unwrap_or_else(PoisonError::into_inner)
            .push((after.cloned(), limit));
        Ok(self
            .deliveries
            .lock()
            .unwrap_or_else(PoisonError::into_inner)
            .iter()
            .filter(|delivery| {
                after.is_none_or(|after| DeliveryPosition::of(delivery).key() > after.key())
            })
            .take(limit)
            .cloned()
            .collect())
    }
}

fn handler<'a>(world: &'a World, waiting: &'a ScriptedWaiting) -> Handler<'a, TestStore> {
    Handler {
        unit_of_work: &world.store,
        waiting,
        deliveries: &world.store,
        couriers: &world.store,
        offers: &world.store,
        offers_of: &world.store,
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

/// Waiting delivery created at `created_at`, as the query returns it.
fn created(world: &World, id: &str, created_at: i64) -> Delivery {
    DeliveryRepository::add(
        &world.store,
        &new_delivery(id).with_created_at(Some(created_at)),
    )
    .expect("seed delivery");
    world.delivery(id)
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
    let waiting = ScriptedWaiting::default();
    on_line(&world, &["courier-1"]);
    waiting.answer(vec![created(&world, "delivery-1", NOW)]);

    let output = handler(&world, &waiting)
        .execute(BATCH)
        .expect("dispatched");

    assert_eq!(offered_couriers(&output), vec![("delivery-1", "courier-1")]);
    assert!(output.without_candidate.is_empty() && output.skipped.is_empty());
    assert_eq!(output.next, None);
    assert_eq!(waiting.calls(), vec![(None, 100)]);
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
}

/// AC-735
#[test]
fn rejected_delivery_is_offered_to_another_courier() {
    let world = World::new();
    let waiting = ScriptedWaiting::default();
    on_line(&world, &["courier-1", "courier-2"]);
    waiting.answer(vec![created(&world, "delivery-1", NOW)]);
    handler(&world, &waiting)
        .execute(BATCH)
        .expect("first offer");
    let mut first = world.store_offer("generated-1");
    assert_eq!(first.courier_id(), &CourierId::new("courier-1"));
    first
        .reject(&CourierId::new("courier-1"), NOW)
        .expect("rejected");
    DeliveryOfferRepository::save(&world.store, &first).expect("save");
    // Without a pending offer the delivery is waiting again.
    waiting.answer(vec![world.delivery("delivery-1")]);

    let output = handler(&world, &waiting)
        .execute(BATCH)
        .expect("dispatched again");

    assert_eq!(offered_couriers(&output), vec![("delivery-1", "courier-2")]);
}

/// AC-735
#[test]
fn expired_delivery_is_offered_to_another_courier() {
    let world = World::new();
    let waiting = ScriptedWaiting::default();
    on_line(&world, &["courier-1", "courier-2"]);
    let delivery = created(&world, "delivery-1", NOW);
    world.offer("offer-1", "delivery-1", "courier-1");
    world.clock.set(NOW + OFFER_TTL + 1);
    let mut expired = world.store_offer("offer-1");
    expired.expire(NOW + OFFER_TTL + 1).expect("expired");
    DeliveryOfferRepository::save(&world.store, &expired).expect("save");
    waiting.answer(vec![delivery]);

    let output = handler(&world, &waiting)
        .execute(BATCH)
        .expect("dispatched");

    assert_eq!(offered_couriers(&output), vec![("delivery-1", "courier-2")]);
    // Everyone was offered it once: the delivery waits for Replan.
    let mut second = world.store_offer("generated-1");
    second
        .reject(&CourierId::new("courier-2"), NOW + OFFER_TTL + 1)
        .expect("rejected");
    DeliveryOfferRepository::save(&world.store, &second).expect("save");
    waiting.answer(vec![world.delivery("delivery-1")]);
    let output = handler(&world, &waiting)
        .execute(BATCH)
        .expect("no one left");
    assert_eq!(output.without_candidate, ids(&["delivery-1"]));
}

/// AC-736
#[test]
fn delivery_waits_until_a_courier_goes_on_line() {
    let world = World::new();
    let waiting = ScriptedWaiting::default();
    waiting.answer(vec![created(&world, "delivery-1", NOW)]);
    world.courier("courier-1", CourierStatus::Unavailable);
    world
        .locator
        .set_candidates(vec![candidate("courier-1", 500)]);

    let output = handler(&world, &waiting).execute(BATCH).expect("no error");

    assert!(output.offered.is_empty());
    assert_eq!(output.without_candidate, ids(&["delivery-1"]));
    assert_eq!(world.delivery_state("delivery-1"), State::Waiting);
    assert!(world.store.outbox_messages().is_empty());
    assert!(world.offers_of("delivery-1").expect("read").is_empty());

    world.courier("courier-2", CourierStatus::Available);
    world.locator.set_candidates(vec![
        candidate("courier-1", 500),
        candidate("courier-2", 500),
    ]);
    let output = handler(&world, &waiting)
        .execute(BATCH)
        .expect("courier on line");
    assert_eq!(offered_couriers(&output), vec![("delivery-1", "courier-2")]);
}

#[test]
fn a_full_batch_continues_after_its_last_delivery() {
    let world = World::new();
    let waiting = ScriptedWaiting::default();
    waiting.answer(vec![
        created(&world, "delivery-a", NOW),
        created(&world, "delivery-b", NOW + 1),
        created(&world, "delivery-c", NOW + 2),
    ]);
    let batch = |batch_size, after| Input { batch_size, after };

    let first = handler(&world, &waiting)
        .execute(batch(2, None))
        .expect("first batch");
    assert_eq!(first.without_candidate, ids(&["delivery-a", "delivery-b"]));
    let next = first.next.expect("full batch");
    assert_eq!(next, DeliveryPosition::of(&world.delivery("delivery-b")));

    let second = handler(&world, &waiting)
        .execute(batch(2, Some(next.clone())))
        .expect("second batch");
    assert_eq!(second.without_candidate, ids(&["delivery-c"]));
    assert_eq!(second.next, None, "a short batch ends the pass");
    assert_eq!(waiting.calls(), vec![(None, 2), (Some(next), 2)]);

    assert_eq!(
        handler(&world, &waiting).execute(batch(0, None)),
        Ok(Output::default())
    );
    assert_eq!(waiting.calls().len(), 2, "an empty batch reads nothing");
}

#[test]
fn concurrently_changed_delivery_is_skipped() {
    let world = World::new();
    let waiting = ScriptedWaiting::default();
    on_line(&world, &["courier-1"]);
    let stale = created(&world, "delivery-1", NOW);
    let second = created(&world, "delivery-2", NOW + 1);
    // Another dispatcher claimed the first delivery after the batch was read.
    DeliveryRepository::save(&world.store, &stale).expect("concurrent change");
    waiting.answer(vec![stale, second]);

    let output = handler(&world, &waiting)
        .execute(BATCH)
        .expect("conflict is not an error");

    assert_eq!(output.skipped, ids(&["delivery-1"]));
    assert_eq!(offered_couriers(&output), vec![("delivery-2", "courier-1")]);
    assert!(world.offers_of("delivery-1").expect("read").is_empty());
    assert_eq!(world.store.rollbacks(), 1);
}

#[test]
fn locator_outage_is_an_error() {
    let world = World::new();
    let waiting = ScriptedWaiting::default();
    waiting.answer(vec![created(&world, "delivery-1", NOW)]);
    world.locator.set_unavailable(true);

    assert!(matches!(
        handler(&world, &waiting).execute(BATCH),
        Err(DeliveryApplicationError::DependencyUnavailable(_))
    ));
    assert_eq!(world.delivery_state("delivery-1"), State::Waiting);
}

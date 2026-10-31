use super::*;
use crate::infrastructure::repository::test_support::TestStore;
use crate::slices::courier::domains::courier::{CourierId, CourierStatus};
use crate::slices::delivery::applications::outbox::OutboxMessage;
use crate::slices::delivery::applications::test_support::{
    NOW, OFFER_TTL, World, candidate, new_delivery,
};
use crate::slices::delivery::domains::delivery::{DomainEvent, OfferState, State};

fn handler(world: &World) -> Handler<'_, TestStore> {
    Handler {
        unit_of_work: &world.store,
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

fn input(delivery: &str) -> Input {
    Input {
        delivery_id: delivery.to_owned(),
    }
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

fn offered(offer: &str, expires_at: i64) -> Output {
    Output::Offered {
        offer_id: DeliveryOfferId::new(offer),
        expires_at_epoch_seconds: expires_at,
    }
}

/// AC-959
#[test]
fn created_delivery_is_offered_at_once() {
    let world = World::new();
    on_line(&world, &["courier-1"]);
    world.waiting_delivery("delivery-1");

    let output = handler(&world)
        .execute(input("delivery-1"))
        .expect("advanced");

    assert_eq!(output, offered("generated-1", NOW + OFFER_TTL));
    assert_eq!(world.offer_state("generated-1"), OfferState::Pending);
    assert_eq!(world.delivery_state("delivery-1"), State::Waiting);
    assert_eq!(
        world.store.outbox_messages(),
        vec![OutboxMessage::delivery(DomainEvent::DeliveryOffered {
            delivery_id: DeliveryId::new("delivery-1"),
            offer_id: DeliveryOfferId::new("generated-1"),
            courier_id: CourierId::new("courier-1"),
            expires_at_epoch_seconds: NOW + OFFER_TTL,
        })]
    );
    assert_eq!(world.store.commits(), 1);
}

/// A wake while the offer is open, the deadline itself included, keeps
/// waiting for the same offer.
#[test]
fn open_offer_is_waited_for() {
    let world = World::new();
    on_line(&world, &["courier-1", "courier-2"]);
    world.waiting_delivery("delivery-1");
    world.offer("offer-1", "delivery-1", "courier-1");
    world.clock.set(NOW + OFFER_TTL);

    let output = handler(&world)
        .execute(input("delivery-1"))
        .expect("advanced");

    assert_eq!(output, offered("offer-1", NOW + OFFER_TTL));
    assert_eq!(world.offers_of("delivery-1").expect("read").len(), 1);
    assert!(world.store.outbox_messages().is_empty());
}

/// AC-962, AC-970: a pending offer past its deadline is expired and the
/// next courier is offered in the same Unit of Work.
#[test]
fn offer_past_its_deadline_expires_before_the_next_offer() {
    let world = World::new();
    on_line(&world, &["courier-1", "courier-2"]);
    world.waiting_delivery("delivery-1");
    world.offer("offer-1", "delivery-1", "courier-1");
    world.clock.set(NOW + OFFER_TTL + 1);

    let output = handler(&world)
        .execute(input("delivery-1"))
        .expect("advanced");

    assert_eq!(output, offered("generated-1", NOW + 2 * OFFER_TTL + 1));
    assert_eq!(world.offer_state("offer-1"), OfferState::Expired);
    assert_eq!(
        world.store_offer("generated-1").courier_id(),
        &CourierId::new("courier-2")
    );
    assert_eq!(
        world.store.outbox_messages()[0],
        OutboxMessage::delivery(DomainEvent::DeliveryOfferExpired {
            delivery_id: DeliveryId::new("delivery-1"),
            offer_id: DeliveryOfferId::new("offer-1"),
            courier_id: CourierId::new("courier-1"),
        })
    );
    assert_eq!(world.store.commits(), 1);
}

/// AC-735: a rejecting courier is not offered the delivery again.
#[test]
fn rejected_delivery_is_offered_to_another_courier() {
    let world = World::new();
    on_line(&world, &["courier-1", "courier-2"]);
    world.waiting_delivery("delivery-1");
    handler(&world)
        .execute(input("delivery-1"))
        .expect("first offer");
    let mut first = world.store_offer("generated-1");
    first
        .reject(&CourierId::new("courier-1"), NOW)
        .expect("rejected");
    DeliveryOfferRepository::save(&world.store, &first).expect("save");

    let output = handler(&world)
        .execute(input("delivery-1"))
        .expect("advanced");

    assert_eq!(output, offered("generated-2", NOW + OFFER_TTL));
    assert_eq!(
        world.store_offer("generated-2").courier_id(),
        &CourierId::new("courier-2")
    );
}

/// AC-964: without a candidate the delivery waits; a courier on line is
/// offered it on the next step.
#[test]
fn delivery_without_candidate_waits_for_a_courier() {
    let world = World::new();
    world.waiting_delivery("delivery-1");
    world.courier("courier-1", CourierStatus::Unavailable);
    world
        .locator
        .set_candidates(vec![candidate("courier-1", 500)]);

    assert_eq!(
        handler(&world).execute(input("delivery-1")),
        Ok(Output::NoCandidate)
    );
    assert!(world.store.outbox_messages().is_empty());
    assert!(world.offers_of("delivery-1").expect("read").is_empty());

    world.courier("courier-2", CourierStatus::Available);
    world.locator.set_candidates(vec![
        candidate("courier-1", 500),
        candidate("courier-2", 500),
    ]);
    assert_eq!(
        handler(&world).execute(input("delivery-1")),
        Ok(offered("generated-1", NOW + OFFER_TTL))
    );
}

#[test]
fn assigned_delivery_waits_and_picked_up_one_finishes() {
    let world = World::new();
    world.assigned_delivery("delivery-1", "courier-1");
    world.picked_up_delivery("delivery-2", "courier-2");

    assert_eq!(
        handler(&world).execute(input("delivery-1")),
        Ok(Output::Assigned)
    );
    assert_eq!(
        handler(&world).execute(input("delivery-2")),
        Ok(Output::Finished)
    );
    assert!(world.store.outbox_messages().is_empty());
}

#[test]
fn cancelled_delivery_finishes() {
    let world = World::new();
    let mut delivery = new_delivery("delivery-1");
    delivery.cancel(None).expect("cancelled");
    DeliveryRepository::add(&world.store, &delivery).expect("seed delivery");

    assert_eq!(
        handler(&world).execute(input("delivery-1")),
        Ok(Output::Finished)
    );
}

#[test]
fn unknown_delivery_is_not_found() {
    let world = World::new();

    assert_eq!(
        handler(&world).execute(input("delivery-1")),
        Err(DeliveryApplicationError::DeliveryNotFound(DeliveryId::new(
            "delivery-1"
        )))
    );
}

#[test]
fn locator_outage_is_an_error() {
    let world = World::new();
    world.waiting_delivery("delivery-1");
    world.locator.set_unavailable(true);

    assert!(matches!(
        handler(&world).execute(input("delivery-1")),
        Err(DeliveryApplicationError::DependencyUnavailable(_))
    ));
    assert_eq!(world.delivery_state("delivery-1"), State::Waiting);
}

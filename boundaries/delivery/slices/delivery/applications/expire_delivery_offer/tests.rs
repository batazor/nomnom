use super::*;
use crate::infrastructure::repository::test_support::TestStore;
use crate::slices::courier::domains::courier::{CourierId, CourierStatus};
use crate::slices::delivery::applications::outbox::OutboxMessage;
use crate::slices::delivery::applications::test_support::{NOW, OFFER_TTL, World};
use crate::slices::delivery::domains::delivery::{DeliveryId, DomainEvent, OfferState};

fn handler(world: &World) -> Handler<'_, TestStore> {
    Handler {
        unit_of_work: &world.store,
        offers: &world.store,
        outbox: &world.store,
        clock: &world.clock,
    }
}

fn input() -> Input {
    Input {
        offer_id: "offer-1".to_owned(),
    }
}

fn output(expired: bool) -> Output {
    Output {
        offer_id: DeliveryOfferId::new("offer-1"),
        expired,
    }
}

fn world() -> World {
    let world = World::new();
    world.waiting_delivery("delivery-1");
    world.courier("courier-1", CourierStatus::Available);
    world.offer("offer-1", "delivery-1", "courier-1");
    world
}

/// AC-962
#[test]
fn pending_offer_past_its_deadline_expires() {
    let world = world();
    world.clock.set(NOW + OFFER_TTL + 1);

    assert_eq!(handler(&world).execute(input()), Ok(output(true)));

    assert_eq!(world.offer_state("offer-1"), OfferState::Expired);
    assert_eq!(
        world.store.outbox_messages(),
        vec![OutboxMessage::delivery(DomainEvent::DeliveryOfferExpired {
            delivery_id: DeliveryId::new("delivery-1"),
            offer_id: DeliveryOfferId::new("offer-1"),
            courier_id: CourierId::new("courier-1"),
        })]
    );
    assert_eq!(world.store.commits(), 1);

    // A repeated timer finds it expired already.
    assert_eq!(handler(&world).execute(input()), Ok(output(false)));
    assert_eq!(world.store.outbox_messages().len(), 1);
}

/// The deadline itself is inclusive: the offer stays open.
#[test]
fn offer_on_its_deadline_stays_pending() {
    let world = world();
    world.clock.set(NOW + OFFER_TTL);

    assert_eq!(handler(&world).execute(input()), Ok(output(false)));
    assert_eq!(world.offer_state("offer-1"), OfferState::Pending);
    assert!(world.store.outbox_messages().is_empty());
}

/// AC-963
#[test]
fn offer_answered_before_the_timer_is_left_as_it_is() {
    let world = world();
    let mut rejected = world.store_offer("offer-1");
    rejected
        .reject(&CourierId::new("courier-1"), NOW)
        .expect("reject");
    DeliveryOfferRepository::save(&world.store, &rejected).expect("save");
    world.clock.set(NOW + OFFER_TTL + 1);

    assert_eq!(handler(&world).execute(input()), Ok(output(false)));
    assert_eq!(world.offer_state("offer-1"), OfferState::Rejected);
    assert!(world.store.outbox_messages().is_empty());
}

#[test]
fn unknown_offer_is_not_found() {
    let world = World::new();

    assert_eq!(
        handler(&world).execute(input()),
        Err(DeliveryApplicationError::DeliveryOfferNotFound(
            DeliveryOfferId::new("offer-1")
        ))
    );
}

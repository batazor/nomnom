use super::*;
use crate::infrastructure::memory::MemoryStore;
use crate::pkg::persistence::RepositoryError;
use crate::slices::courier::domains::courier::{CourierId, CourierStatus};
use crate::slices::delivery::applications::outbox::OutboxMessage;
use crate::slices::delivery::applications::test_support::{NOW, OFFER_TTL, World};
use crate::slices::delivery::domains::delivery::{DeliveryId, DomainEvent, OfferState};

fn handler(world: &World) -> Handler<'_, MemoryStore> {
    Handler {
        unit_of_work: &world.store,
        offers: &world.store,
        outbox: &world.store,
        clock: &world.clock,
    }
}

fn expired_fact(offer: &str, courier: &str) -> OutboxMessage {
    OutboxMessage::delivery(DomainEvent::DeliveryOfferExpired {
        delivery_id: DeliveryId::new("delivery-1"),
        offer_id: DeliveryOfferId::new(offer),
        courier_id: CourierId::new(courier),
    })
}

fn ids(values: &[&str]) -> Vec<DeliveryOfferId> {
    values.iter().copied().map(DeliveryOfferId::new).collect()
}

fn world() -> World {
    let world = World::new();
    world.waiting_delivery("delivery-1");
    world.courier("courier-1", CourierStatus::Available);
    world.courier("courier-2", CourierStatus::Available);
    world
}

const BATCH: Input = Input { batch_size: 100 };

#[test]
fn expires_only_pending_offers_past_their_deadline() {
    let world = world();
    world.offer("offer-1", "delivery-1", "courier-1");
    world.offer("offer-2", "delivery-1", "courier-2");
    let mut rejected = world.store_offer("offer-2");
    rejected
        .reject(&CourierId::new("courier-2"), NOW)
        .expect("reject");
    DeliveryOfferRepository::save(&world.store, &rejected).expect("save");

    // On the deadline the offer is still open.
    world.clock.set(NOW + OFFER_TTL);
    assert_eq!(handler(&world).execute(BATCH), Ok(Output::default()));
    assert_eq!(world.offer_state("offer-1"), OfferState::Pending);

    world.clock.set(NOW + OFFER_TTL + 1);
    let output = handler(&world).execute(BATCH).expect("expired");

    assert_eq!(output.expired, ids(&["offer-1"]));
    assert!(output.skipped.is_empty());
    assert!(!output.more);
    assert_eq!(world.offer_state("offer-1"), OfferState::Expired);
    assert_eq!(world.offer_state("offer-2"), OfferState::Rejected);
    assert_eq!(
        world.store.outbox_messages(),
        vec![expired_fact("offer-1", "courier-1")]
    );
    assert_eq!(world.store.commits(), 1);
}

#[test]
fn repeated_run_is_idempotent() {
    let world = world();
    world.offer("offer-1", "delivery-1", "courier-1");
    world.clock.set(NOW + OFFER_TTL + 1);
    handler(&world).execute(BATCH).expect("first");

    let repeat = handler(&world).execute(BATCH).expect("repeat");

    assert_eq!(repeat, Output::default());
    assert_eq!(world.offer_state("offer-1"), OfferState::Expired);
    assert_eq!(
        world.store.outbox_messages(),
        vec![expired_fact("offer-1", "courier-1")]
    );
    assert_eq!(world.store.commits(), 1);
}

#[test]
fn batches_are_limited_and_oldest_first() {
    let world = world();
    world.offer("offer-1", "delivery-1", "courier-1");
    world.offer("offer-2", "delivery-1", "courier-2");
    world.clock.set(NOW + OFFER_TTL + 1);

    let first = handler(&world)
        .execute(Input { batch_size: 1 })
        .expect("first batch");
    assert_eq!(first.expired, ids(&["offer-1"]));
    assert!(first.more);
    let second = handler(&world)
        .execute(Input { batch_size: 1 })
        .expect("second batch");
    assert_eq!(second.expired, ids(&["offer-2"]));
    assert_eq!(
        handler(&world).execute(Input { batch_size: 0 }),
        Ok(Output::default())
    );
    assert_eq!(world.offer_state("offer-2"), OfferState::Expired);
}

/// Offers repository whose batch is read before a concurrent writer rejects
/// the first offer, so the expiry save hits a version conflict.
struct ConcurrentRejection<'a> {
    store: &'a MemoryStore,
}

impl DeliveryOfferRepository for ConcurrentRejection<'_> {
    fn get(&self, id: &DeliveryOfferId) -> Result<Option<DeliveryOffer>, RepositoryError> {
        DeliveryOfferRepository::get(self.store, id)
    }

    fn list_for_delivery(
        &self,
        delivery_id: &DeliveryId,
    ) -> Result<Vec<DeliveryOffer>, RepositoryError> {
        self.store.list_for_delivery(delivery_id)
    }

    fn list_expired_pending(
        &self,
        now_epoch_seconds: i64,
        limit: usize,
    ) -> Result<Vec<DeliveryOffer>, RepositoryError> {
        let batch = self.store.list_expired_pending(now_epoch_seconds, limit)?;
        let mut concurrent = batch[0].clone();
        // The addressee rejected right at the deadline, before the job saved.
        concurrent
            .reject(
                &concurrent.courier_id().clone(),
                concurrent.expires_at_epoch_seconds(),
            )
            .expect("concurrent reject");
        DeliveryOfferRepository::save(self.store, &concurrent)?;
        Ok(batch)
    }

    fn add(&self, offer: &DeliveryOffer) -> Result<(), RepositoryError> {
        DeliveryOfferRepository::add(self.store, offer)
    }

    fn save(&self, offer: &DeliveryOffer) -> Result<(), RepositoryError> {
        DeliveryOfferRepository::save(self.store, offer)
    }
}

#[test]
fn concurrently_closed_offer_is_skipped() {
    let world = world();
    world.offer("offer-1", "delivery-1", "courier-1");
    world.offer("offer-2", "delivery-1", "courier-2");
    world.clock.set(NOW + OFFER_TTL + 1);
    let offers = ConcurrentRejection {
        store: &world.store,
    };

    let output = Handler {
        unit_of_work: &world.store,
        offers: &offers,
        outbox: &world.store,
        clock: &world.clock,
    }
    .execute(BATCH)
    .expect("conflict is not an error");

    assert_eq!(output.skipped, ids(&["offer-1"]));
    assert_eq!(output.expired, ids(&["offer-2"]));
    assert_eq!(world.offer_state("offer-1"), OfferState::Rejected);
    assert_eq!(world.offer_state("offer-2"), OfferState::Expired);
    assert_eq!(
        world.store.outbox_messages(),
        vec![expired_fact("offer-2", "courier-2")]
    );
    assert_eq!(world.store.rollbacks(), 1);
}

#[test]
fn storage_outage_is_an_error() {
    let world = world();
    world.offer("offer-1", "delivery-1", "courier-1");
    world.clock.set(NOW + OFFER_TTL + 1);
    world.store.set_unavailable(true);

    assert!(matches!(
        handler(&world).execute(BATCH),
        Err(DeliveryApplicationError::DependencyUnavailable(_))
    ));
    world.store.set_unavailable(false);
    assert_eq!(world.offer_state("offer-1"), OfferState::Pending);
}

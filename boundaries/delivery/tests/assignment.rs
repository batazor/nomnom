//! Integration-тесты атомарности Courier Assignment (FR-310, FR-485) против
//! настоящего PostgreSQL: Accept Delivery Offer занимает курьера через порт
//! слайса `courier` в той же транзакции, что offer, `Delivery` и Outbox;
//! конкурентные шаги Assignment Workflow предлагают доставку один раз
//! (FR-704); оффер ждёт блокировку доставки и не проскакивает мимо принятия.
//! PostgreSQL из testcontainers (`tests/support`); идентификаторы уникальны,
//! поэтому база общая.

mod support;

use delivery::slices::courier::domains::courier::MarketId;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::{Arc, Barrier, Mutex, mpsc};
use std::time::{Duration, SystemTime, UNIX_EPOCH};

use delivery::infrastructure::memory::{
    ManualClock, RecordingAssignment, RecordingRelationships, StaticAuthorization,
    StaticCourierLocator,
};
use delivery::infrastructure::repository::Store;
use delivery::pkg::authz::Subject;
use delivery::pkg::persistence::RepositoryError;
use delivery::slices::courier::domains::courier::{
    Courier, CourierId, CourierName, CourierRepository, CourierStatus,
};
use delivery::slices::delivery::applications::assignment::advance;
use delivery::slices::delivery::applications::courier_locator::CourierCandidate;
use delivery::slices::delivery::applications::error::DeliveryApplicationError;
use delivery::slices::delivery::applications::offer::accept::{Handler, Input};
use delivery::slices::delivery::applications::offer::offer_delivery;
use delivery::slices::delivery::applications::offers_of_delivery::OffersOfDelivery;
use delivery::slices::delivery::applications::policy::AssignmentPolicy;
use delivery::slices::delivery::domains::delivery::{
    Delivery, DeliveryId, DeliveryOffer, DeliveryOfferId, DeliveryOfferRepository,
    DeliveryRepository, OfferState, OrderId, State,
};
use delivery::slices::delivery::infrastructure::DeliveryIntegrationEvents;

const NOW: i64 = 1_000;

/// Store over the migrated test database.
fn store() -> Store {
    Store::connect(&support::database())
        .expect("connect")
        .with_outbox_mapper(Arc::new(DeliveryIntegrationEvents))
}

fn unique(prefix: &str) -> String {
    static NEXT: AtomicUsize = AtomicUsize::new(0);
    let nanos = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .expect("clock")
        .as_nanos();
    format!("{prefix}-{nanos}-{}", NEXT.fetch_add(1, Ordering::Relaxed))
}

/// Waiting delivery with one pending offer to an available courier.
struct Seed {
    delivery_id: DeliveryId,
    offer_id: DeliveryOfferId,
    courier_id: CourierId,
}

fn seed(store: &Store) -> Seed {
    let seed = Seed {
        delivery_id: DeliveryId::new(unique("delivery")),
        offer_id: DeliveryOfferId::new(unique("offer")),
        courier_id: CourierId::new(unique("courier")),
    };
    CourierRepository::add(
        store,
        &Courier::new(
            seed.courier_id.clone(),
            CourierName::new("Timur").expect("courier name"),
            MarketId::new("berlin").expect("market id"),
        ),
    )
    .expect("courier");
    DeliveryRepository::add(
        store,
        &Delivery::new(
            seed.delivery_id.clone(),
            OrderId::new(unique("order")),
            MarketId::new("berlin").expect("market id"),
        ),
    )
    .expect("delivery");
    let offer = DeliveryOffer::new(
        seed.offer_id.clone(),
        seed.delivery_id.clone(),
        seed.courier_id.clone(),
        NOW,
        NOW + 60,
    )
    .expect("offer");
    DeliveryOfferRepository::add(store, &offer).expect("offer");
    seed
}

fn accept(
    store: &Store,
    seed: &Seed,
    relationships: &RecordingRelationships,
) -> Result<(), DeliveryApplicationError> {
    accept_through(store, store, seed, relationships)
}

fn accept_through(
    store: &Store,
    deliveries: &dyn DeliveryRepository,
    seed: &Seed,
    relationships: &RecordingRelationships,
) -> Result<(), DeliveryApplicationError> {
    let authorization = StaticAuthorization::allow_all();
    let locator = StaticCourierLocator::new(vec![CourierCandidate {
        courier_id: seed.courier_id.clone(),
        market_id: MarketId::new("berlin").expect("market id"),
        within_delivery_zone: true,
        distance_to_pickup_meters: 1_000,
        active_assignments: 0,
    }]);
    let clock = ManualClock::new(NOW);
    Handler {
        authorization: &authorization,
        unit_of_work: store,
        offers: store,
        offers_of: store,
        deliveries,
        couriers: store,
        outbox: store,
        locator: &locator,
        clock: &clock,
        policy: AssignmentPolicy {
            maximum_distance_meters: 5_000,
            maximum_active_assignments: 1,
            offer_ttl_seconds: 60,
        },
        relationships,
        assignment: &RecordingAssignment::new(),
    }
    .execute(
        &Subject::User(seed.courier_id.as_str().to_owned()),
        Input {
            offer_id: seed.offer_id.as_str().to_owned(),
            courier_id: seed.courier_id.as_str().to_owned(),
            expected_pickup_at_epoch_seconds: None,
        },
    )
    .map(drop)
}

fn courier_status(store: &Store, seed: &Seed) -> CourierStatus {
    CourierRepository::get(store, &seed.courier_id)
        .expect("get courier")
        .expect("courier")
        .status()
}

fn delivery_state(store: &Store, seed: &Seed) -> State {
    DeliveryRepository::get(store, &seed.delivery_id)
        .expect("get delivery")
        .expect("delivery")
        .state()
}

fn offer_state(store: &Store, seed: &Seed) -> OfferState {
    DeliveryOfferRepository::get(store, &seed.offer_id)
        .expect("get offer")
        .expect("offer")
        .state()
}

/// AC-549: the courier becomes busy through the `courier` port and is
/// committed together with the offer and the delivery.
#[test]
fn acceptance_commits_offer_delivery_and_courier_together() {
    let store = store();
    let seed = seed(&store);

    accept(&store, &seed, &RecordingRelationships::new()).expect("offer accepted");

    assert_eq!(courier_status(&store, &seed), CourierStatus::Busy);
    assert_eq!(delivery_state(&store, &seed), State::Assigned);
    assert_eq!(offer_state(&store, &seed), OfferState::Accepted);
}

/// FR-310: a failure inside the transaction leaves the courier available, the
/// offer pending and the delivery waiting.
#[test]
fn failed_acceptance_rolls_courier_back_with_offer_and_delivery() {
    let store = store();
    let seed = seed(&store);

    assert!(matches!(
        accept(
            &store,
            &seed,
            &RecordingRelationships::failing("spicedb offline")
        ),
        Err(DeliveryApplicationError::DependencyUnavailable(_))
    ));

    assert_eq!(courier_status(&store, &seed), CourierStatus::Available);
    assert_eq!(delivery_state(&store, &seed), State::Waiting);
    assert_eq!(offer_state(&store, &seed), OfferState::Pending);
}

/// Delivery reads of two workflow steps: both read the delivery before
/// either offers it.
struct SameRead {
    store: Arc<Store>,
    barrier: Arc<Barrier>,
}

impl DeliveryRepository for SameRead {
    fn get(&self, id: &DeliveryId) -> Result<Option<Delivery>, RepositoryError> {
        let delivery = DeliveryRepository::get(&*self.store, id)?;
        self.barrier.wait();
        Ok(delivery)
    }

    fn find_by_order(&self, order_id: &OrderId) -> Result<Option<Delivery>, RepositoryError> {
        self.store.find_by_order(order_id)
    }

    fn add(&self, delivery: &Delivery) -> Result<(), RepositoryError> {
        DeliveryRepository::add(&*self.store, delivery)
    }

    fn save(&self, delivery: &Delivery) -> Result<(), RepositoryError> {
        DeliveryRepository::save(&*self.store, delivery)
    }

    fn hold(&self, delivery: &Delivery) -> Result<(), RepositoryError> {
        DeliveryRepository::hold(&*self.store, delivery)
    }
}

/// FR-704: steps racing for one delivery make one offer; the loser fails
/// with a concurrent modification, which the workflow retries.
#[test]
fn concurrent_steps_offer_the_delivery_once() {
    let store = Arc::new(store());
    let courier_id = CourierId::new(unique("courier"));
    CourierRepository::add(
        &*store,
        &Courier::new(
            courier_id.clone(),
            CourierName::new("Timur").expect("courier name"),
            MarketId::new("berlin").expect("market id"),
        ),
    )
    .expect("courier");
    let delivery = Delivery::new(
        DeliveryId::new(unique("delivery")),
        OrderId::new(unique("order")),
        MarketId::new("berlin").expect("market id"),
    );
    DeliveryRepository::add(&*store, &delivery).expect("delivery");

    let steps = 2;
    let barrier = Arc::new(Barrier::new(steps));
    let handles: Vec<_> = (0..steps)
        .map(|_| {
            let deliveries = SameRead {
                store: Arc::clone(&store),
                barrier: Arc::clone(&barrier),
            };
            let store = Arc::clone(&store);
            let courier_id = courier_id.clone();
            let delivery_id = delivery.id().as_str().to_owned();
            std::thread::spawn(move || {
                let locator = StaticCourierLocator::new(vec![CourierCandidate {
                    courier_id,
                    market_id: MarketId::new("berlin").expect("market id"),
                    within_delivery_zone: true,
                    distance_to_pickup_meters: 1_000,
                    active_assignments: 0,
                }]);
                advance::Handler {
                    unit_of_work: &*store,
                    deliveries: &deliveries,
                    couriers: &*store,
                    offers: &*store,
                    offers_of: &*store,
                    outbox: &*store,
                    locator: &locator,
                    clock: &ManualClock::new(NOW),
                    policy: AssignmentPolicy {
                        maximum_distance_meters: 5_000,
                        maximum_active_assignments: 1,
                        offer_ttl_seconds: 60,
                    },
                }
                .execute(advance::Input { delivery_id })
            })
        })
        .collect();
    let outcomes: Vec<_> = handles
        .into_iter()
        .map(|handle| handle.join().expect("step"))
        .collect();

    assert_eq!(
        outcomes
            .iter()
            .filter(|outcome| matches!(outcome, Ok(advance::Output::Offered { .. })))
            .count(),
        1
    );
    assert_eq!(
        outcomes
            .iter()
            .filter(|outcome| **outcome == Err(DeliveryApplicationError::ConcurrentModification))
            .count(),
        1
    );
    let ids = store.offer_ids_of(delivery.id()).expect("read");
    assert_eq!(ids.len(), 1);
    let offer = DeliveryOfferRepository::get(&*store, &ids[0])
        .expect("read offer")
        .expect("offer");
    assert_eq!(offer.courier_id(), &courier_id);
    assert_eq!(offer.state(), OfferState::Pending);
}

/// Keeps the transaction open after the lock of the delivery is taken, so
/// another transaction meets it.
struct PausedHold<'a> {
    store: &'a Store,
    held: mpsc::Sender<()>,
    release: Mutex<mpsc::Receiver<()>>,
}

impl DeliveryRepository for PausedHold<'_> {
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

    fn hold(&self, delivery: &Delivery) -> Result<(), RepositoryError> {
        DeliveryRepository::hold(self.store, delivery)?;
        self.held.send(()).expect("held");
        self.release
            .lock()
            .expect("release")
            .recv()
            .expect("release");
        Ok(())
    }
}

fn offer(
    store: &Store,
    deliveries: &dyn DeliveryRepository,
    seed: &Seed,
    courier_id: &CourierId,
) -> Result<offer_delivery::Output, DeliveryApplicationError> {
    offer_delivery::Handler {
        authorization: &StaticAuthorization::allow_all(),
        unit_of_work: store,
        deliveries,
        couriers: store,
        offers: store,
        offers_of: store,
        outbox: store,
        clock: &ManualClock::new(NOW),
        policy: AssignmentPolicy {
            maximum_distance_meters: 5_000,
            maximum_active_assignments: 1,
            offer_ttl_seconds: 60,
        },
    }
    .execute(
        &Subject::Service("dispatcher".to_owned()),
        offer_delivery::Input {
            delivery_id: seed.delivery_id.as_str().to_owned(),
            courier_id: courier_id.as_str().to_owned(),
        },
    )
}

fn second_courier(store: &Store) -> CourierId {
    let courier_id = CourierId::new(unique("courier"));
    CourierRepository::add(
        store,
        &Courier::new(
            courier_id.clone(),
            CourierName::new("Timur").expect("courier name"),
            MarketId::new("berlin").expect("market id"),
        ),
    )
    .expect("courier");
    courier_id
}

/// An offer made while an acceptance holds the delivery waits for its commit
/// and fails: it cannot stay pending on an assigned delivery.
#[test]
fn offer_racing_an_acceptance_waits_and_fails() {
    let store = store();
    let seed = seed(&store);
    let other = second_courier(&store);
    let (held_tx, held) = mpsc::channel();
    let (release, release_rx) = mpsc::channel();
    let paused = PausedHold {
        store: &store,
        held: held_tx,
        release: Mutex::new(release_rx),
    };

    std::thread::scope(|scope| {
        let accepting =
            scope.spawn(|| accept_through(&store, &paused, &seed, &RecordingRelationships::new()));
        held.recv().expect("acceptance holds the delivery");
        let offering = scope.spawn(|| offer(&store, &store, &seed, &other));
        std::thread::sleep(Duration::from_millis(500));
        assert!(!offering.is_finished(), "the offer waits for the lock");
        release.send(()).expect("release");

        accepting.join().expect("accept").expect("accepted");
        assert_eq!(
            offering.join().expect("offer").map(drop),
            Err(DeliveryApplicationError::ConcurrentModification)
        );
    });

    assert_eq!(delivery_state(&store, &seed), State::Assigned);
    assert_eq!(
        store.offer_ids_of(&seed.delivery_id).expect("read"),
        vec![seed.offer_id.clone()]
    );
}

/// Two offers to one courier: the second waits and returns the first.
#[test]
fn offers_racing_for_a_pair_make_one() {
    let store = store();
    let seed = seed(&store);
    let other = second_courier(&store);
    let (held_tx, held) = mpsc::channel();
    let (release, release_rx) = mpsc::channel();
    let paused = PausedHold {
        store: &store,
        held: held_tx,
        release: Mutex::new(release_rx),
    };

    let (first, second) = std::thread::scope(|scope| {
        let first = scope.spawn(|| offer(&store, &paused, &seed, &other));
        held.recv().expect("first offer holds the delivery");
        let second = scope.spawn(|| offer(&store, &store, &seed, &other));
        std::thread::sleep(Duration::from_millis(500));
        assert!(!second.is_finished(), "the offer waits for the lock");
        release.send(()).expect("release");
        (
            first.join().expect("first").expect("first offer"),
            second.join().expect("second").expect("second offer"),
        )
    });

    assert!(first.created);
    assert!(!second.created);
    assert_eq!(second.offer_id, first.offer_id);
    assert_eq!(
        store.offer_ids_of(&seed.delivery_id).expect("read").len(),
        2
    );
}

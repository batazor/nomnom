//! Integration-тесты атомарности Courier Assignment (FR-310, FR-485) против
//! настоящего PostgreSQL: Accept Delivery Offer занимает курьера через порт
//! слайса `courier` в той же транзакции, что offer, `Delivery` и Outbox;
//! конкурентные Dispatch Deliveries предлагают доставку один раз (FR-582).
//! Запускаются только при заданном `DATABASE_URL`:
//!
//! `DATABASE_URL=postgres://postgres:postgres@localhost:5432/postgres cargo test --test postgres_assignment`
//!
//! Без переменной тесты завершаются сразу. Идентификаторы уникальны, поэтому
//! база может быть общей.

use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::{Arc, Barrier};
use std::time::{SystemTime, UNIX_EPOCH};

use delivery::infrastructure::memory::{
    ManualClock, RecordingRelationships, SequentialIdGenerator, StaticAuthorization,
    StaticCourierLocator,
};
use delivery::infrastructure::postgres::{PostgresStore, migrate};
use delivery::pkg::application::Subject;
use delivery::pkg::config::DatabaseConfig;
use delivery::pkg::persistence::RepositoryError;
use delivery::slices::courier::domains::courier::{
    Courier, CourierId, CourierRepository, CourierStatus,
};
use delivery::slices::delivery::applications::accept_delivery_offer::{Handler, Input};
use delivery::slices::delivery::applications::courier_locator::CourierCandidate;
use delivery::slices::delivery::applications::dispatch_deliveries::{
    self, DeliveryPosition, WaitingDeliveries,
};
use delivery::slices::delivery::applications::error::DeliveryApplicationError;
use delivery::slices::delivery::applications::policy::AssignmentPolicy;
use delivery::slices::delivery::domains::delivery::{
    Address, Delivery, DeliveryId, DeliveryOffer, DeliveryOfferId, DeliveryOfferRepository,
    DeliveryRepository, OfferState, OrderId, State,
};
use delivery::slices::delivery::infrastructure::DeliveryIntegrationEvents;

const NOW: i64 = 1_000;

fn config() -> Option<DatabaseConfig> {
    let dsn = std::env::var("DATABASE_URL")
        .ok()
        .filter(|dsn| !dsn.is_empty())?;
    Some(DatabaseConfig {
        url: dsn,
        max_conns: 4,
        ..DatabaseConfig::default()
    })
}

/// Migrated store, or `None` when `DATABASE_URL` is unset (test skipped).
fn store() -> Option<PostgresStore> {
    let config = config()?;
    migrate(&config).expect("migrate");
    Some(
        PostgresStore::connect(&config)
            .expect("connect")
            .with_outbox_mapper(Arc::new(DeliveryIntegrationEvents)),
    )
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

fn seed(store: &PostgresStore) -> Seed {
    let seed = Seed {
        delivery_id: DeliveryId::new(unique("delivery")),
        offer_id: DeliveryOfferId::new(unique("offer")),
        courier_id: CourierId::new(unique("courier")),
    };
    CourierRepository::add(store, &Courier::new(seed.courier_id.clone())).expect("courier");
    DeliveryRepository::add(
        store,
        &Delivery::new(
            seed.delivery_id.clone(),
            OrderId::new(unique("order")),
            Address::new("Restaurant street 1").expect("pickup"),
            Address::new("Customer street 2").expect("dropoff"),
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
    store: &PostgresStore,
    seed: &Seed,
    relationships: &RecordingRelationships,
) -> Result<(), DeliveryApplicationError> {
    let authorization = StaticAuthorization::allow_all();
    let locator = StaticCourierLocator::new(vec![CourierCandidate {
        courier_id: seed.courier_id.clone(),
        within_delivery_zone: true,
        distance_to_pickup_meters: 1_000,
        active_assignments: 0,
    }]);
    let clock = ManualClock::new(NOW);
    Handler {
        authorization: &authorization,
        unit_of_work: store,
        offers: store,
        deliveries: store,
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
    }
    .execute(
        &Subject::User(seed.courier_id.as_str().to_owned()),
        Input {
            offer_id: seed.offer_id.as_str().to_owned(),
            courier_id: seed.courier_id.as_str().to_owned(),
        },
    )
    .map(drop)
}

fn courier_status(store: &PostgresStore, seed: &Seed) -> CourierStatus {
    CourierRepository::get(store, &seed.courier_id)
        .expect("get courier")
        .expect("courier")
        .status()
}

fn delivery_state(store: &PostgresStore, seed: &Seed) -> State {
    DeliveryRepository::get(store, &seed.delivery_id)
        .expect("get delivery")
        .expect("delivery")
        .state()
}

fn offer_state(store: &PostgresStore, seed: &Seed) -> OfferState {
    DeliveryOfferRepository::get(store, &seed.offer_id)
        .expect("get offer")
        .expect("offer")
        .state()
}

/// AC-549: the courier becomes busy through the `courier` port and is
/// committed together with the offer and the delivery.
#[test]
fn acceptance_commits_offer_delivery_and_courier_together() {
    let Some(store) = store() else { return };
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
    let Some(store) = store() else { return };
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

/// Batch reads of two dispatchers: both read the batch before either
/// dispatches it.
struct SameBatch {
    store: Arc<PostgresStore>,
    barrier: Arc<Barrier>,
}

impl WaitingDeliveries for SameBatch {
    fn list_waiting_without_pending_offer(
        &self,
        after: Option<&DeliveryPosition>,
        limit: usize,
    ) -> Result<Vec<Delivery>, RepositoryError> {
        let batch = self
            .store
            .list_waiting_without_pending_offer(after, limit)?;
        self.barrier.wait();
        Ok(batch)
    }
}

/// FR-582: dispatchers racing for one delivery make one offer; the loser
/// skips it.
#[test]
fn concurrent_dispatchers_offer_the_delivery_once() {
    let Some(store) = store() else { return };
    let store = Arc::new(store);
    let courier_id = CourierId::new(unique("courier"));
    CourierRepository::add(&*store, &Courier::new(courier_id.clone())).expect("courier");
    // A creation time far in the past, unique per run, puts this delivery
    // first in the batch after `start`.
    let nanos = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .expect("clock")
        .as_nanos();
    let created_at = -9_000_000_000 - i64::try_from(nanos % 1_000_000_000).expect("fits") * 2;
    let delivery = Delivery::new(
        DeliveryId::new(unique("delivery")),
        OrderId::new(unique("order")),
        Address::new("Restaurant street 1").expect("pickup"),
        Address::new("Customer street 2").expect("dropoff"),
    )
    .with_created_at(Some(created_at));
    DeliveryRepository::add(&*store, &delivery).expect("delivery");
    let start = DeliveryPosition {
        created_at_epoch_seconds: created_at - 1,
        delivery_id: DeliveryId::new(""),
    };

    let dispatchers = 2;
    let barrier = Arc::new(Barrier::new(dispatchers));
    let handles: Vec<_> = (0..dispatchers)
        .map(|dispatcher| {
            let waiting = SameBatch {
                store: Arc::clone(&store),
                barrier: Arc::clone(&barrier),
            };
            let store = Arc::clone(&store);
            let start = start.clone();
            let courier_id = courier_id.clone();
            std::thread::spawn(move || {
                let locator = StaticCourierLocator::new(vec![CourierCandidate {
                    courier_id,
                    within_delivery_zone: true,
                    distance_to_pickup_meters: 1_000,
                    active_assignments: 0,
                }]);
                dispatch_deliveries::Handler {
                    unit_of_work: &*store,
                    waiting: &waiting,
                    deliveries: &*store,
                    couriers: &*store,
                    offers: &*store,
                    outbox: &*store,
                    locator: &locator,
                    ids: &SequentialIdGenerator::new(unique(&format!("offer-{dispatcher}"))),
                    clock: &ManualClock::new(NOW),
                    policy: AssignmentPolicy {
                        maximum_distance_meters: 5_000,
                        maximum_active_assignments: 1,
                        offer_ttl_seconds: 60,
                    },
                }
                .execute(dispatch_deliveries::Input {
                    batch_size: 1,
                    after: Some(start),
                })
            })
        })
        .collect();
    let outputs: Vec<_> = handles
        .into_iter()
        .map(|handle| handle.join().expect("dispatcher").expect("no error"))
        .collect();

    assert_eq!(
        outputs
            .iter()
            .map(|output| output.offered.len())
            .sum::<usize>(),
        1
    );
    assert_eq!(
        outputs
            .iter()
            .filter(|output| output.skipped == vec![delivery.id().clone()])
            .count(),
        1
    );
    let offers = DeliveryOfferRepository::list_for_delivery(&*store, delivery.id()).expect("read");
    assert_eq!(offers.len(), 1);
    assert_eq!(offers[0].courier_id(), &courier_id);
    assert_eq!(offers[0].state(), OfferState::Pending);
}

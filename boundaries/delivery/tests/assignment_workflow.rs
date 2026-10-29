//! Integration-тест Assignment Workflow (FR-704…FR-707) против настоящих
//! Temporal dev server и PostgreSQL из testcontainers: worker процесса
//! `delivery-worker` и Temporal-клиент use cases над одним хранилищем.
//! Offer живёт 2 s, поэтому истечение по таймеру видно за секунды.

mod support;

use std::sync::Arc;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::time::{Duration, Instant, SystemTime, UNIX_EPOCH};

use delivery::di::worker::{TemporalWorker, assignment_steps};
use delivery::infrastructure::memory::{
    ManualClock, RecordingRelationships, StaticAuthorization, StaticCourierLocator,
};
use delivery::infrastructure::repository::Store;
use delivery::infrastructure::temporal::{AssignmentWorkflow, TemporalAssignment, workflow_id};
use delivery::pkg::application::Subject;
use delivery::pkg::lifecycle::Worker;
use delivery::slices::courier::applications::candidate_wake::CandidateWake;
use delivery::slices::courier::applications::update_courier_location::{
    CourierLocationStore, CourierPosition,
};
use delivery::slices::courier::domains::courier::{
    Courier, CourierId, CourierName, CourierRepository, Location, MarketId,
};
use delivery::slices::delivery::applications::assignment_process::AssignmentProcess;
use delivery::slices::delivery::applications::courier_locator::CourierCandidate;
use delivery::slices::delivery::applications::offers_of_delivery::OffersOfDelivery;
use delivery::slices::delivery::applications::policy::AssignmentPolicy;
use delivery::slices::delivery::applications::{accept_delivery_offer, confirm_pickup};
use delivery::slices::delivery::domains::delivery::{
    Delivery, DeliveryId, DeliveryOffer, DeliveryOfferRepository, DeliveryRepository, OfferState,
    OrderId, State,
};
use delivery::slices::delivery::infrastructure::DeliveryIntegrationEvents;
use temporalio_client::{
    Client, ClientOptions, Connection, ConnectionOptions, WorkflowGetResultOptions,
};
use tokio::sync::watch;

const OFFER_TTL: i64 = 2;
const WAIT: Duration = Duration::from_secs(30);

fn unique(prefix: &str) -> String {
    static NEXT: AtomicUsize = AtomicUsize::new(0);
    let nanos = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .expect("clock")
        .as_nanos();
    format!("{prefix}-{nanos}-{}", NEXT.fetch_add(1, Ordering::Relaxed))
}

fn store() -> Arc<Store> {
    Arc::new(
        Store::connect(&support::database())
            .expect("connect")
            .with_outbox_mapper(Arc::new(DeliveryIntegrationEvents)),
    )
}

/// Available courier of `market` with a stored position: a Courier Candidate.
fn on_line(store: &Store, market: &MarketId, courier: &str) -> CourierId {
    let courier_id = CourierId::new(courier);
    CourierRepository::add(
        store,
        &Courier::new(
            courier_id.clone(),
            CourierName::new("Timur").expect("courier name"),
            market.clone(),
        ),
    )
    .expect("courier");
    CourierLocationStore::record(
        store,
        &CourierPosition {
            courier_id: courier_id.clone(),
            location: Location::new(52.52, 13.405).expect("location"),
            recorded_at_epoch_seconds: 0,
        },
        0,
    )
    .expect("position");
    courier_id
}

fn offers(store: &Store, delivery_id: &DeliveryId) -> Vec<DeliveryOffer> {
    store
        .offer_ids_of(delivery_id)
        .expect("offer ids")
        .iter()
        .map(|id| {
            DeliveryOfferRepository::get(store, id)
                .expect("read offer")
                .expect("offer")
        })
        .collect()
}

/// Polls the store until `done` holds; the workflow runs on its own.
async fn eventually(what: &str, done: impl Fn() -> bool) {
    let deadline = Instant::now() + WAIT;
    while !done() {
        assert!(Instant::now() < deadline, "timed out waiting for {what}");
        tokio::time::sleep(Duration::from_millis(200)).await;
    }
}

fn pending_to(store: &Store, delivery_id: &DeliveryId, courier: &CourierId) -> bool {
    offers(store, delivery_id)
        .iter()
        .any(|offer| offer.courier_id() == courier && offer.state() == OfferState::Pending)
}

/// AC-959, AC-962, AC-964, AC-960: offer at once, expiry by the timer and the
/// next courier, waiting without a candidate until a courier goes on line,
/// assignment and the end of the workflow after Pickup.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn workflow_searches_a_courier_until_pickup() {
    let store = store();
    let temporal = support::off_runtime(support::temporal);
    let worker = TemporalWorker::start(
        &temporal,
        assignment_steps(
            Arc::clone(&store),
            AssignmentPolicy {
                maximum_distance_meters: 5_000,
                maximum_active_assignments: 1,
                offer_ttl_seconds: OFFER_TTL,
            },
        ),
    )
    .expect("worker");
    let (stop, stop_rx) = watch::channel(false);
    let running = tokio::spawn(worker.run(stop_rx));
    let process = TemporalAssignment::connect(&temporal, Arc::clone(&store) as _).expect("client");

    // A Market of its own keeps the couriers of other tests out.
    let nanos = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .expect("clock")
        .as_nanos();
    let market = MarketId::new(&format!("w{}", nanos % 1_000_000_000_000)).expect("market id");
    let first = on_line(&store, &market, &unique("a-courier"));
    let second = on_line(&store, &market, &unique("b-courier"));
    let delivery = Delivery::new(
        DeliveryId::new(unique("delivery")),
        OrderId::new(unique("order")),
        market.clone(),
    );
    DeliveryRepository::add(&*store, &delivery).expect("delivery");
    let delivery_id = delivery.id().clone();

    // AC-959: Create Delivery starts the workflow; the first courier is
    // offered at once. A repeated start keeps the running workflow.
    process.start(&delivery_id).expect("start");
    process.start(&delivery_id).expect("repeat start");
    eventually("the first offer", || {
        pending_to(&store, &delivery_id, &first)
    })
    .await;

    // AC-962: nobody answers; the timer expires the offer and the second
    // courier is offered.
    eventually("the second offer", || {
        pending_to(&store, &delivery_id, &second)
    })
    .await;
    assert_eq!(offers(&store, &delivery_id)[0].state(), OfferState::Expired);

    // Both expired: no candidate is left, the workflow waits.
    eventually("both offers expired", || {
        offers(&store, &delivery_id)
            .iter()
            .all(|offer| offer.state() == OfferState::Expired)
    })
    .await;
    tokio::time::sleep(Duration::from_secs(1)).await;
    assert_eq!(offers(&store, &delivery_id).len(), 2);

    // AC-964: a courier on line wakes the delivery and is offered it.
    let third = on_line(&store, &market, &unique("c-courier"));
    process.courier_available(&third).expect("wake");
    eventually("the offer to the courier on line", || {
        pending_to(&store, &delivery_id, &third)
    })
    .await;
    let offer = offers(&store, &delivery_id)
        .into_iter()
        .find(|offer| offer.courier_id() == &third)
        .expect("offer to the third courier");

    let authorization = StaticAuthorization::allow_all();
    let clock = ManualClock::new(offer.offered_at_epoch_seconds());
    let accepted = {
        let store = Arc::clone(&store);
        let third = third.clone();
        let offer_id = offer.id().as_str().to_owned();
        let process = &process;
        tokio::task::block_in_place(|| {
            accept_delivery_offer::Handler {
                authorization: &authorization,
                unit_of_work: &*store,
                offers: &*store,
                offers_of: &*store,
                deliveries: &*store,
                couriers: &*store,
                outbox: &*store,
                locator: &StaticCourierLocator::new(vec![CourierCandidate {
                    courier_id: third.clone(),
                    market_id: market.clone(),
                    within_delivery_zone: true,
                    distance_to_pickup_meters: 100,
                    active_assignments: 0,
                }]),
                clock: &clock,
                policy: AssignmentPolicy {
                    maximum_distance_meters: 5_000,
                    maximum_active_assignments: 1,
                    offer_ttl_seconds: OFFER_TTL,
                },
                relationships: &RecordingRelationships::new(),
                assignment: process,
            }
            .execute(
                &Subject::User(third.as_str().to_owned()),
                accept_delivery_offer::Input {
                    offer_id,
                    courier_id: third.as_str().to_owned(),
                    expected_pickup_at_epoch_seconds: None,
                },
            )
        })
    };
    accepted.expect("accepted");

    // AC-960: Pickup ends the workflow.
    tokio::task::block_in_place(|| {
        confirm_pickup::Handler {
            authorization: &authorization,
            unit_of_work: &*store,
            deliveries: &*store,
            outbox: &*store,
            clock: &clock,
            assignment: &process,
        }
        .execute(
            &Subject::User(third.as_str().to_owned()),
            confirm_pickup::Input {
                delivery_id: delivery_id.as_str().to_owned(),
                courier_id: third.as_str().to_owned(),
                expected_delivered_at_epoch_seconds: None,
            },
        )
    })
    .expect("picked up");
    assert_eq!(
        DeliveryRepository::get(&*store, &delivery_id)
            .expect("read")
            .expect("delivery")
            .state(),
        State::PickedUp
    );

    let connection = Connection::connect(
        ConnectionOptions::new(
            url::Url::parse(&format!("http://{}", temporal.host_port)).expect("url"),
        )
        .build(),
    )
    .await
    .expect("connect");
    let client = Client::new(connection, ClientOptions::new("default").build()).expect("client");
    tokio::time::timeout(
        WAIT,
        client
            .get_workflow_handle::<AssignmentWorkflow>(workflow_id(delivery_id.as_str()))
            .get_result(WorkflowGetResultOptions::default()),
    )
    .await
    .expect("workflow finishes")
    .expect("workflow completed");
    // A wake after the end is not an error.
    process.courier_available(&third).expect("late wake");

    stop.send(true).expect("stop");
    running.await.expect("join").expect("worker stopped");
}

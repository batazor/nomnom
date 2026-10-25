use std::collections::VecDeque;
use std::sync::Mutex;
use std::sync::atomic::{AtomicBool, AtomicI64, Ordering};

use super::*;
use crate::infrastructure::memory::RecordingRelationships;
use crate::infrastructure::postgres::test_support::TestStore;
use crate::slices::courier::applications::{register_courier, update_courier_location};
use crate::slices::delivery::applications::{create_delivery, dispatch_deliveries};
use crate::slices::delivery::domains::delivery::State;
use crate::slices::simulator::applications::work::{
    ActiveDeliveryWork, PendingOfferWork, SimulatorWork,
};
use crate::slices::simulator::domains::simulator::SimulatorStateRepository;

use super::super::infrastructure::local_authorization;

const SCHEDULE: SimulatorSchedule = SimulatorSchedule {
    interval: Duration::from_secs(5),
    min_delay: Duration::from_secs(30),
    max_delay: Duration::from_secs(120),
};

struct FakeClock(AtomicI64);

impl FakeClock {
    fn at(now: i64) -> Arc<Self> {
        Arc::new(Self(AtomicI64::new(now)))
    }

    fn advance(&self, seconds: i64) {
        self.0.fetch_add(seconds, Ordering::SeqCst);
    }
}

impl Clock for FakeClock {
    fn now_epoch_seconds(&self) -> i64 {
        self.0.load(Ordering::SeqCst)
    }
}

/// Delays and percents in the given order; delays record their bounds.
#[derive(Default)]
struct Scripted {
    delays: VecDeque<Duration>,
    percents: VecDeque<u32>,
    bounds: Arc<Mutex<Vec<(Duration, Duration)>>>,
}

impl Randomness for Scripted {
    fn delay(&mut self, min: Duration, max: Duration) -> Duration {
        self.bounds.lock().expect("bounds").push((min, max));
        self.delays.pop_front().unwrap_or(min)
    }

    fn percent(&mut self) -> u32 {
        self.percents.pop_front().expect("scripted percent")
    }

    fn index(&mut self, _len: usize) -> usize {
        0
    }
}

/// Switch and work of the Simulator as a test fake of its store port.
#[derive(Default)]
struct FakeWork {
    enabled: AtomicBool,
    offers: Mutex<Vec<PendingOfferWork>>,
    deliveries: Mutex<Vec<ActiveDeliveryWork>>,
}

impl SimulatorStateRepository for FakeWork {
    fn enabled(&self) -> Result<bool, RepositoryError> {
        Ok(self.enabled.load(Ordering::SeqCst))
    }

    fn set_enabled(&self, enabled: bool) -> Result<(), RepositoryError> {
        self.enabled.store(enabled, Ordering::SeqCst);
        Ok(())
    }
}

impl SimulatorWork for FakeWork {
    fn pending_offers(&self) -> Result<Vec<PendingOfferWork>, RepositoryError> {
        Ok(self.offers.lock().expect("offers").clone())
    }

    fn active_deliveries(&self) -> Result<Vec<ActiveDeliveryWork>, RepositoryError> {
        Ok(self.deliveries.lock().expect("deliveries").clone())
    }
}

#[derive(Clone, Debug, Eq, PartialEq)]
enum Call {
    Accept(String, String),
    Reject(String, String),
    Pickup(String, String),
    Deliver(String, String),
    Fail(String, String, DeliveryFailureCode),
}

#[derive(Default)]
struct RecordingActions {
    calls: Mutex<Vec<Call>>,
    fail: AtomicBool,
}

impl RecordingActions {
    fn record(&self, call: Call) -> Result<(), DeliveryApplicationError> {
        self.calls.lock().expect("calls").push(call);
        if self.fail.load(Ordering::SeqCst) {
            return Err(DeliveryApplicationError::PermissionDenied);
        }
        Ok(())
    }

    fn calls(&self) -> Vec<Call> {
        self.calls.lock().expect("calls").clone()
    }
}

impl CourierActions for RecordingActions {
    fn accept_offer(
        &self,
        courier: &CourierId,
        offer: &DeliveryOfferId,
    ) -> Result<(), DeliveryApplicationError> {
        self.record(Call::Accept(courier.as_str().into(), offer.as_str().into()))
    }

    fn reject_offer(
        &self,
        courier: &CourierId,
        offer: &DeliveryOfferId,
    ) -> Result<(), DeliveryApplicationError> {
        self.record(Call::Reject(courier.as_str().into(), offer.as_str().into()))
    }

    fn confirm_pickup(
        &self,
        courier: &CourierId,
        delivery: &DeliveryId,
    ) -> Result<(), DeliveryApplicationError> {
        self.record(Call::Pickup(
            courier.as_str().into(),
            delivery.as_str().into(),
        ))
    }

    fn confirm_delivery(
        &self,
        courier: &CourierId,
        delivery: &DeliveryId,
    ) -> Result<(), DeliveryApplicationError> {
        self.record(Call::Deliver(
            courier.as_str().into(),
            delivery.as_str().into(),
        ))
    }

    fn report_failure(
        &self,
        courier: &CourierId,
        delivery: &DeliveryId,
        reason: DeliveryFailureCode,
    ) -> Result<(), DeliveryApplicationError> {
        self.record(Call::Fail(
            courier.as_str().into(),
            delivery.as_str().into(),
            reason,
        ))
    }
}

struct Harness {
    work: Arc<FakeWork>,
    actions: Arc<RecordingActions>,
    clock: Arc<FakeClock>,
    simulator: Simulator,
}

fn harness(random: Scripted) -> Harness {
    let work = Arc::new(FakeWork::default());
    work.set_enabled(true).expect("enable");
    let actions = Arc::new(RecordingActions::default());
    let clock = FakeClock::at(1_000);
    let simulator = Simulator::new(
        Arc::clone(&work) as _,
        Arc::clone(&actions) as _,
        Arc::clone(&clock) as _,
        Box::new(random),
        SCHEDULE,
    );
    Harness {
        work,
        actions,
        clock,
        simulator,
    }
}

fn offer(id: &str, courier: &str, expires_at: i64) -> PendingOfferWork {
    PendingOfferWork {
        offer_id: DeliveryOfferId::new(id),
        courier_id: CourierId::new(courier),
        expires_at_epoch_seconds: expires_at,
    }
}

fn delivery(id: &str, courier: &str, step: DeliveryStep) -> ActiveDeliveryWork {
    ActiveDeliveryWork {
        delivery_id: DeliveryId::new(id),
        courier_id: CourierId::new(courier),
        step,
    }
}

fn scripted(delays: &[u64], percents: &[u32]) -> Scripted {
    Scripted {
        delays: delays.iter().copied().map(Duration::from_secs).collect(),
        percents: percents.iter().copied().collect(),
        ..Scripted::default()
    }
}

#[test]
fn acts_only_when_the_delay_from_first_sight_is_over() {
    let mut h = harness(scripted(&[40], &[0]));
    h.work.deliveries.lock().expect("deliveries").push(delivery(
        "d-1",
        "c-1",
        DeliveryStep::Pickup,
    ));

    h.simulator.tick();
    h.clock.advance(39);
    h.simulator.tick();
    assert!(h.actions.calls().is_empty(), "not before firstSeen + delay");

    h.clock.advance(1);
    h.simulator.tick();
    assert_eq!(
        h.actions.calls(),
        [Call::Pickup("c-1".into(), "d-1".into())]
    );
}

#[test]
fn outcome_is_selected_by_the_weights_of_each_entity() {
    // percent below the weight → main action, at or above → the other one.
    let mut h = harness(scripted(&[30; 6], &[84, 85, 94, 95, 94, 95]));
    h.work
        .offers
        .lock()
        .expect("offers")
        .extend([offer("o-1", "c-1", 10_000), offer("o-2", "c-2", 10_000)]);
    h.simulator.tick();
    h.clock.advance(30);
    h.simulator.tick();
    let mut calls = h.actions.calls();
    calls.sort_by_key(|call| format!("{call:?}"));
    // HashMap order is arbitrary: compare as a set of (kind) per percent.
    assert_eq!(calls.len(), 2);
    assert!(calls.iter().any(|call| matches!(call, Call::Accept(..))));
    assert!(calls.iter().any(|call| matches!(call, Call::Reject(..))));

    h.work.offers.lock().expect("offers").clear();
    h.work.deliveries.lock().expect("deliveries").push(delivery(
        "d-1",
        "c-1",
        DeliveryStep::Pickup,
    ));
    h.simulator.tick();
    h.clock.advance(30);
    h.simulator.tick();
    h.work.deliveries.lock().expect("deliveries")[0] = delivery("d-2", "c-2", DeliveryStep::Pickup);
    h.simulator.tick();
    h.clock.advance(30);
    h.simulator.tick();
    h.work.deliveries.lock().expect("deliveries")[0] =
        delivery("d-3", "c-3", DeliveryStep::Handover);
    h.simulator.tick();
    h.clock.advance(30);
    h.simulator.tick();
    h.work.deliveries.lock().expect("deliveries")[0] =
        delivery("d-4", "c-4", DeliveryStep::Handover);
    h.simulator.tick();
    h.clock.advance(30);
    h.simulator.tick();

    assert_eq!(
        h.actions.calls()[2..],
        [
            Call::Pickup("c-1".into(), "d-1".into()),
            Call::Fail(
                "c-2".into(),
                "d-2".into(),
                DeliveryFailureCode::CustomerUnreachable
            ),
            Call::Deliver("c-3".into(), "d-3".into()),
            Call::Fail(
                "c-4".into(),
                "d-4".into(),
                DeliveryFailureCode::CustomerUnreachable
            ),
        ]
    );
}

/// AC-764
#[test]
fn disabled_simulator_does_nothing_and_forgets_its_schedule() {
    let mut h = harness(scripted(&[30, 60], &[0]));
    h.work.deliveries.lock().expect("deliveries").push(delivery(
        "d-1",
        "c-1",
        DeliveryStep::Handover,
    ));
    h.simulator.tick();
    h.work.set_enabled(false).expect("disable");
    h.clock.advance(30);
    h.simulator.tick();
    assert!(h.actions.calls().is_empty());

    // Enabled again: the delay is counted from the new first sight.
    h.work.set_enabled(true).expect("enable");
    h.simulator.tick();
    h.clock.advance(59);
    h.simulator.tick();
    assert!(h.actions.calls().is_empty());
    h.clock.advance(1);
    h.simulator.tick();
    assert_eq!(
        h.actions.calls(),
        [Call::Deliver("c-1".into(), "d-1".into())]
    );
}

#[test]
fn failed_use_case_drops_the_entry_and_the_simulator_continues() {
    let mut h = harness(scripted(&[30, 30], &[0, 0]));
    h.actions.fail.store(true, Ordering::SeqCst);
    h.work.deliveries.lock().expect("deliveries").push(delivery(
        "d-1",
        "c-1",
        DeliveryStep::Pickup,
    ));
    h.simulator.tick();
    h.clock.advance(30);
    h.simulator.tick();
    assert_eq!(h.actions.calls().len(), 1);

    // The entity is still there: it is scheduled anew, not retried at once.
    h.simulator.tick();
    assert_eq!(h.actions.calls().len(), 1);
    h.clock.advance(30);
    h.simulator.tick();
    assert_eq!(h.actions.calls().len(), 2);
}

#[test]
fn offer_delay_ends_before_the_offer_expires() {
    let random = scripted(&[], &[0, 0]);
    let bounds = Arc::clone(&random.bounds);
    let mut h = harness(random);
    // 60s TTL: at most 55s, the last pass before expiry.
    h.work
        .offers
        .lock()
        .expect("offers")
        .extend([offer("o-1", "c-1", 1_060), offer("o-2", "c-2", 1_020)]);

    h.simulator.tick();
    assert_eq!(
        *bounds.lock().expect("bounds"),
        [(Duration::from_secs(30), Duration::from_secs(55))]
    );
    // o-2 expires before MIN_DELAY: acted on the last pass before expiry.
    h.clock.advance(15);
    h.simulator.tick();
    assert_eq!(
        h.actions.calls(),
        [Call::Accept("c-2".into(), "o-2".into())]
    );
}

fn operator() -> Subject {
    Subject::User("operator-1".to_owned())
}

/// The Simulator acts through the real use cases of the process: a
/// dispatched offer is accepted on behalf of its courier.
#[test]
fn accepts_a_dispatched_offer_through_the_use_case() {
    let store = Arc::new(TestStore::new());
    let use_cases = Arc::new(super::super::build_over_store(
        &store,
        Box::new(local_authorization()),
        Box::new(RecordingRelationships::new()),
    ));
    let couriers = use_cases.couriers();
    couriers
        .register_courier(
            &operator(),
            register_courier::Input {
                courier_id: "courier-1".to_owned(),
            },
        )
        .expect("courier registered");
    couriers
        .update_courier_location(
            &operator(),
            update_courier_location::Input {
                courier_id: "courier-1".to_owned(),
                latitude: 52.37,
                longitude: 4.89,
            },
        )
        .expect("position recorded");
    let created = use_cases
        .deliveries()
        .create_delivery(
            &operator(),
            create_delivery::Input {
                order_id: "order-1".to_owned(),
                pickup_address: "Pickup street 1".to_owned(),
            },
        )
        .expect("delivery created");
    use_cases
        .deliveries()
        .dispatch_deliveries(dispatch_deliveries::Input {
            batch_size: 10,
            after: None,
        })
        .expect("dispatched");
    store.set_enabled(true).expect("enable simulator");
    let clock = FakeClock::at(crate::di::infrastructure::SystemClock.now_epoch_seconds());
    let mut simulator = Simulator::new(
        Arc::clone(&store) as _,
        Arc::new(UseCaseCourierActions::new(Arc::clone(&use_cases))),
        Arc::clone(&clock) as _,
        Box::new(scripted(&[0], &[0])),
        SimulatorSchedule {
            interval: Duration::from_secs(1),
            min_delay: Duration::from_secs(1),
            max_delay: Duration::from_secs(2),
        },
    );

    // Delay 0: the offer is due on the pass that first sees it.
    simulator.tick();

    let delivery = crate::slices::delivery::domains::delivery::DeliveryRepository::get(
        &*store,
        &created.delivery_id,
    )
    .expect("read delivery")
    .expect("delivery exists");
    assert_eq!(delivery.state(), State::Assigned);
    assert_eq!(
        store.active_deliveries().expect("work"),
        [ActiveDeliveryWork {
            delivery_id: created.delivery_id,
            courier_id: CourierId::new("courier-1"),
            step: DeliveryStep::Pickup,
        }]
    );
}

#[tokio::test]
async fn worker_stops_with_the_main_worker() {
    struct IdleWorker;

    impl Worker for IdleWorker {
        async fn run(self, mut stop: watch::Receiver<bool>) -> io::Result<()> {
            crate::pkg::lifecycle::wait_for_stop(&mut stop).await;
            Ok(())
        }
    }

    let h = harness(Scripted::default());
    let (sender, receiver) = watch::channel(false);
    let worker = tokio::spawn(
        SimulatorWorker::new(IdleWorker, h.simulator, Duration::from_millis(5)).run(receiver),
    );
    tokio::time::sleep(Duration::from_millis(20)).await;
    sender.send(true).expect("stop");
    tokio::time::timeout(Duration::from_secs(5), worker)
        .await
        .expect("stops promptly")
        .expect("joined")
        .expect("worker ok");
}

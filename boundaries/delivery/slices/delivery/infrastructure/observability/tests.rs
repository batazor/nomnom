use crate::slices::courier::domains::courier::MarketId;
use std::sync::atomic::{AtomicI64, Ordering};

use opentelemetry::metrics::MeterProvider;
use opentelemetry_sdk::metrics::data::{AggregatedMetrics, MetricData, ResourceMetrics};
use opentelemetry_sdk::metrics::{InMemoryMetricExporter, PeriodicReader, SdkMeterProvider};

use super::*;
use crate::infrastructure::memory::{ManualClock, SequentialIdGenerator, StaticAuthorization};
use crate::infrastructure::repository::test_support::TestStore;
use crate::pkg::application::{ScopedUnitOfWork, Subject};
use crate::slices::courier::domains::courier::CourierId;
use crate::slices::delivery::applications::create_delivery;
use crate::slices::delivery::domains::delivery::{
    DeliveryFailureCode, DeliveryFailureReason, DeliveryId, DeliveryOfferId, OrderId,
};

const NOW: i64 = 10_000;

struct Harness {
    provider: SdkMeterProvider,
    exporter: InMemoryMetricExporter,
    observed: ObservedOutbox,
    now: Arc<AtomicI64>,
}

impl Harness {
    fn new() -> Self {
        Self::with_reasons(&[])
    }

    fn with_reasons(reasons: &[&str]) -> Self {
        let exporter = InMemoryMetricExporter::default();
        let provider = SdkMeterProvider::builder()
            .with_reader(PeriodicReader::builder(exporter.clone()).build())
            .build();
        let now = Arc::new(AtomicI64::new(NOW));
        let clock = Arc::clone(&now);
        let metrics = DomainMetrics::new(
            &provider.meter("test"),
            reasons,
            Arc::new(move || clock.load(Ordering::SeqCst)),
        );
        let observed =
            ObservedOutbox::new(metrics, TransitionMetrics::new(&provider.meter("test")));
        Self {
            provider,
            exporter,
            observed,
            now,
        }
    }

    /// Latest cumulative export; `None` while nothing was recorded.
    fn collect(&self) -> Option<ResourceMetrics> {
        self.provider.force_flush().expect("flush");
        self.exporter.get_finished_metrics().expect("metrics").pop()
    }

    /// Sum of counter `name` over the data points carrying `attribute`
    /// (every data point when `None`).
    fn count(&self, name: &str, attribute: Option<(&str, &str)>) -> u64 {
        let Some(metrics) = self.collect() else {
            return 0;
        };
        let mut total = 0;
        for metric in metrics
            .scope_metrics()
            .flat_map(|scope| scope.metrics())
            .filter(|metric| metric.name() == name)
        {
            let AggregatedMetrics::U64(MetricData::Sum(sum)) = metric.data() else {
                panic!("{name} is not a u64 counter");
            };
            for point in sum.data_points() {
                let matches = attribute.is_none_or(|(key, value)| {
                    point
                        .attributes()
                        .any(|kv| kv.key.as_str() == key && kv.value.as_str() == value)
                });
                if matches {
                    total += point.value();
                }
            }
        }
        total
    }

    /// `(count, sum)` of histogram `name`.
    fn histogram(&self, name: &str) -> (u64, f64) {
        let Some(metrics) = self.collect() else {
            return (0, 0.0);
        };
        metrics
            .scope_metrics()
            .flat_map(|scope| scope.metrics())
            .filter(|metric| metric.name() == name)
            .find_map(|metric| match metric.data() {
                AggregatedMetrics::F64(MetricData::Histogram(histogram)) => histogram
                    .data_points()
                    .next()
                    .map(|point| (point.count(), point.sum())),
                _ => None,
            })
            .unwrap_or((0, 0.0))
    }

    /// Runs `events` through the decorated Outbox inside one decorated Unit
    /// of Work of `scope`; `commit` decides the outcome of the work.
    fn unit_of_work(
        &self,
        scope: &dyn TransactionScope,
        outbox: &dyn DeliveryOutbox,
        events: &[DomainEvent],
        commit: bool,
    ) -> Result<(), UnitOfWorkError> {
        self.observed.run_scoped(scope, &mut || {
            for event in events {
                self.observed
                    .add(outbox, OutboxMessage::delivery(event.clone()))
                    .expect("outbox add");
            }
            commit
        })
    }
}

fn delivery_id() -> DeliveryId {
    DeliveryId::new("delivery-1")
}

fn courier_id() -> CourierId {
    CourierId::new("courier-1")
}

fn created() -> DomainEvent {
    DomainEvent::DeliveryCreated {
        delivery_id: delivery_id(),
        order_id: OrderId::new("order-1"),
        created_at_epoch_seconds: NOW,
        market_id: MarketId::new("berlin").expect("market id"),
    }
}

fn assigned(created_at: Option<i64>) -> DomainEvent {
    DomainEvent::CourierAssignedToDelivery {
        delivery_id: delivery_id(),
        order_id: OrderId::new("order-1"),
        courier_id: courier_id(),
        created_at_epoch_seconds: created_at,
        expected_pickup_at_epoch_seconds: None,
        market_id: MarketId::new("berlin").expect("market id"),
    }
}

fn completed(created_at: Option<i64>) -> DomainEvent {
    DomainEvent::DeliveryCompleted {
        delivery_id: delivery_id(),
        order_id: OrderId::new("order-1"),
        courier_id: courier_id(),
        created_at_epoch_seconds: created_at,
        market_id: MarketId::new("berlin").expect("market id"),
    }
}

fn failed(reason: &str) -> DomainEvent {
    DomainEvent::DeliveryFailed {
        delivery_id: delivery_id(),
        order_id: OrderId::new("order-1"),
        courier_id: courier_id(),
        reason: DeliveryFailureReason::new(reason, Some("free text detail")).expect("reason"),
        market_id: MarketId::new("berlin").expect("market id"),
    }
}

fn offer(accepted: bool) -> DomainEvent {
    let (delivery_id, offer_id, courier_id) =
        (delivery_id(), DeliveryOfferId::new("offer-1"), courier_id());
    if accepted {
        DomainEvent::DeliveryOfferAccepted {
            delivery_id,
            offer_id,
            courier_id,
        }
    } else {
        DomainEvent::DeliveryOfferRejected {
            delivery_id,
            offer_id,
            courier_id,
        }
    }
}

fn offer_expired() -> DomainEvent {
    DomainEvent::DeliveryOfferExpired {
        delivery_id: delivery_id(),
        offer_id: DeliveryOfferId::new("offer-1"),
        courier_id: courier_id(),
    }
}

#[test]
fn committed_events_are_counted_by_metric() {
    let harness = Harness::new();
    let store = TestStore::new();
    let events = [
        created(),
        assigned(Some(NOW - 90)),
        DomainEvent::CourierUnassignedFromDelivery {
            delivery_id: delivery_id(),
            order_id: OrderId::new("order-1"),
            courier_id: courier_id(),
            market_id: MarketId::new("berlin").expect("market id"),
        },
        offer(true),
        offer(false),
        offer_expired(),
        DomainEvent::DeliveryPickedUp {
            delivery_id: delivery_id(),
            order_id: OrderId::new("order-1"),
            courier_id: courier_id(),
            expected_delivered_at_epoch_seconds: None,
            market_id: MarketId::new("berlin").expect("market id"),
        },
        completed(Some(NOW - 1_800)),
        failed("no_access"),
        DomainEvent::DeliveryCancelled {
            delivery_id: delivery_id(),
            order_id: OrderId::new("order-1"),
            released_courier_id: None,
            reason: None,
            market_id: MarketId::new("berlin").expect("market id"),
        },
    ];

    harness
        .unit_of_work(&store, &store, &events, true)
        .expect("committed");

    assert_eq!(store.outbox_messages().len(), events.len());
    assert_eq!(harness.count("nomnom.delivery.created", None), 1);
    assert_eq!(harness.count("nomnom.delivery.assignments", None), 1);
    assert_eq!(
        harness.count("nomnom.delivery.reassignments", Some((REASON, "other"))),
        1
    );
    assert_eq!(
        harness.count("nomnom.delivery.offers", Some((OUTCOME, "accepted"))),
        1
    );
    assert_eq!(
        harness.count("nomnom.delivery.offers", Some((OUTCOME, "rejected"))),
        1
    );
    assert_eq!(
        harness.count("nomnom.delivery.offers", Some((OUTCOME, "expired"))),
        1
    );
    assert_eq!(harness.count("nomnom.delivery.offers", None), 3);
    assert_eq!(harness.count("nomnom.delivery.pickups", None), 1);
    assert_eq!(harness.count("nomnom.delivery.completed", None), 1);
    assert_eq!(harness.count("nomnom.delivery.failed", None), 1);
}

/// A waiting Delivery stored outside any Unit of Work.
fn stored_delivery(store: &TestStore) -> Delivery {
    let delivery = Delivery::new(
        delivery_id(),
        OrderId::new("order-1"),
        MarketId::new("berlin").expect("market id"),
    );
    DeliveryRepository::add(store, &delivery).expect("add");
    delivery
}

/// Loads the stored Delivery, assigns the courier and saves it through the
/// decorator inside one Unit of Work; `commit` decides the outcome.
fn assign_in_unit_of_work(harness: &Harness, store: &TestStore, commit: bool) {
    harness
        .observed
        .run_scoped(store, &mut || {
            let mut delivery = harness
                .observed
                .get_delivery(store, &delivery_id())
                .expect("get")
                .expect("stored");
            delivery.assign(courier_id(), None).expect("assign");
            harness
                .observed
                .save_delivery(store, &delivery)
                .expect("save");
            commit
        })
        .expect("unit of work");
}

#[test]
fn saved_state_change_is_the_applied_transition_after_commit() {
    let harness = Harness::new();
    let store = TestStore::new();
    stored_delivery(&store);

    assign_in_unit_of_work(&harness, &store, false);
    assert_eq!(harness.count("nomnom.fsm.transitions", None), 0);

    assign_in_unit_of_work(&harness, &store, true);
    for attribute in [
        ("nomnom.fsm.aggregate_type", "delivery"),
        ("nomnom.fsm.from_state", "waiting"),
        ("nomnom.fsm.trigger", "assign"),
        ("nomnom.fsm.to_state", "assigned"),
        ("nomnom.fsm.result", "applied"),
        ("nomnom.fsm.terminal", "false"),
    ] {
        assert_eq!(
            harness.count("nomnom.fsm.transitions", Some(attribute)),
            1,
            "{attribute:?}"
        );
    }
}

#[test]
fn save_without_state_change_or_outside_unit_of_work_records_no_transition() {
    let harness = Harness::new();
    let store = TestStore::new();
    stored_delivery(&store);

    harness
        .observed
        .run_scoped(&store, &mut || {
            let loaded = harness
                .observed
                .get_delivery(&store, &delivery_id())
                .expect("get")
                .expect("stored");
            harness
                .observed
                .save_delivery(&store, &loaded)
                .expect("save");
            true
        })
        .expect("committed");
    // Without a Unit of Work the loaded state is not tracked.
    let mut delivery = harness
        .observed
        .get_delivery(&store, &delivery_id())
        .expect("get")
        .expect("stored");
    delivery.assign(courier_id(), None).expect("assign");
    harness
        .observed
        .save_delivery(&store, &delivery)
        .expect("save");

    assert_eq!(harness.count("nomnom.fsm.transitions", None), 0);
}

#[test]
fn rolled_back_or_failed_commit_records_nothing() {
    let harness = Harness::new();
    let store = TestStore::new();

    harness
        .unit_of_work(&store, &store, &[created()], false)
        .expect("rolled back");
    store.fail_next_commit(UnitOfWorkError::CommitFailed("connection lost".to_owned()));
    assert!(
        harness
            .unit_of_work(&store, &store, &[created()], true)
            .is_err()
    );

    assert!(store.outbox_messages().is_empty());
    assert_eq!(harness.count("nomnom.delivery.created", None), 0);
    // The next Unit of Work on the same thread starts clean.
    harness
        .unit_of_work(&store, &store, &[created()], true)
        .expect("committed");
    assert_eq!(harness.count("nomnom.delivery.created", None), 1);
}

/// Unit of Work that supports nesting: the work joins the running scope.
#[derive(Default)]
struct NestingScope {
    store: TestStore,
}

impl TransactionScope for NestingScope {
    fn run_scoped(&self, work: &mut dyn FnMut() -> bool) -> Result<(), UnitOfWorkError> {
        work();
        Ok(())
    }
}

#[test]
fn nested_unit_of_work_is_recorded_once_at_the_outer_commit() {
    let harness = Harness::new();
    let scope = NestingScope::default();
    let mut recorded_inside = None;

    harness
        .observed
        .run_scoped(&scope, &mut || {
            harness
                .unit_of_work(&scope, &scope.store, &[created()], true)
                .expect("nested commit");
            // A nested scope that rolls back drops only its own events.
            harness
                .unit_of_work(&scope, &scope.store, &[offer(false)], false)
                .expect("nested rollback");
            recorded_inside = Some(harness.count("nomnom.delivery.created", None));
            true
        })
        .expect("outer commit");

    assert_eq!(recorded_inside, Some(0));
    assert_eq!(harness.count("nomnom.delivery.created", None), 1);
    assert_eq!(harness.count("nomnom.delivery.offers", None), 0);
}

/// Ports of a use case whose Unit of Work and Outbox are decorated.
struct ObservedPorts<'a> {
    store: &'a TestStore,
    observed: &'a ObservedOutbox,
}

impl TransactionScope for ObservedPorts<'_> {
    fn run_scoped(&self, work: &mut dyn FnMut() -> bool) -> Result<(), UnitOfWorkError> {
        self.observed.run_scoped(self.store, work)
    }
}

impl DeliveryOutbox for ObservedPorts<'_> {
    fn add(&self, message: OutboxMessage) -> Result<(), RepositoryError> {
        self.observed.add(self.store, message)
    }
}

#[test]
fn idempotent_repeat_is_recorded_once() {
    let harness = Harness::new();
    let store = TestStore::new();
    let ports = ObservedPorts {
        store: &store,
        observed: &harness.observed,
    };
    let unit_of_work = ScopedUnitOfWork::new(&ports);
    let authorization = StaticAuthorization::allow_all();
    let ids = SequentialIdGenerator::new("delivery");
    let clock = ManualClock::new(NOW);
    let handler = create_delivery::Handler {
        authorization: &authorization,
        unit_of_work: &unit_of_work,
        deliveries: &store,
        outbox: &ports,
        ids: &ids,
        clock: &clock,
    };
    let input = || create_delivery::Input {
        order_id: "order-1".to_owned(),
        market_id: "berlin".to_owned(),
    };
    let subject = Subject::Service("fulfillment".to_owned());

    assert!(handler.execute(&subject, input()).expect("created").created);
    assert!(!handler.execute(&subject, input()).expect("replay").created);

    assert_eq!(harness.count("nomnom.delivery.created", None), 1);
}

#[test]
fn failure_reason_outside_the_closed_set_is_recorded_as_other() {
    let harness = Harness::with_reasons(&["customer_unreachable"]);
    let store = TestStore::new();

    harness
        .unit_of_work(
            &store,
            &store,
            &[failed("customer_unreachable"), failed("no_access")],
            true,
        )
        .expect("committed");

    assert_eq!(
        harness.count(
            "nomnom.delivery.failed",
            Some((REASON, "customer_unreachable"))
        ),
        1
    );
    assert_eq!(
        harness.count("nomnom.delivery.failed", Some((REASON, OTHER_REASON))),
        1
    );
}

/// With the catalog as the closed set (as in the composition root) every
/// code is its own `nomnom.reason`; the detail is never an attribute.
#[test]
fn failure_reason_codes_of_the_catalog_are_recorded_as_is() {
    let harness = Harness::with_reasons(&DeliveryFailureCode::CODES);
    let store = TestStore::new();
    let events: Vec<DomainEvent> = DeliveryFailureCode::CODES
        .iter()
        .map(|code| failed(code))
        .collect();

    harness
        .unit_of_work(&store, &store, &events, true)
        .expect("committed");

    for code in DeliveryFailureCode::CODES {
        assert_eq!(
            harness.count("nomnom.delivery.failed", Some((REASON, code))),
            1,
            "{code}"
        );
    }
    let metrics = harness.collect().expect("metrics");
    for metric in metrics
        .scope_metrics()
        .flat_map(|scope| scope.metrics())
        .filter(|metric| metric.name() == "nomnom.delivery.failed")
    {
        let AggregatedMetrics::U64(MetricData::Sum(sum)) = metric.data() else {
            panic!("not a counter");
        };
        for point in sum.data_points() {
            let keys: Vec<_> = point
                .attributes()
                .map(|kv| kv.key.as_str().to_owned())
                .collect();
            assert_eq!(keys, vec![REASON.to_owned()]);
            assert!(
                point
                    .attributes()
                    .all(|kv| kv.value.as_str() != "free text detail")
            );
        }
    }
}

#[test]
fn durations_are_measured_from_creation_at_commit() {
    let harness = Harness::new();
    let store = TestStore::new();
    harness.now.store(NOW + 120, Ordering::SeqCst);

    harness
        .unit_of_work(
            &store,
            &store,
            // A Delivery without a creation time has no duration.
            &[
                assigned(Some(NOW)),
                completed(Some(NOW - 3_480)),
                completed(None),
            ],
            true,
        )
        .expect("committed");

    assert_eq!(
        harness.histogram("nomnom.delivery.assignment.latency"),
        (1, 120.0)
    );
    assert_eq!(harness.histogram("nomnom.delivery.duration"), (1, 3_600.0));
    assert_eq!(harness.count("nomnom.delivery.completed", None), 2);
}

#[test]
fn outbox_write_outside_a_unit_of_work_is_recorded_at_once() {
    let harness = Harness::new();
    let store = TestStore::new();

    harness
        .observed
        .add(&store, OutboxMessage::delivery(created()))
        .expect("added");

    assert_eq!(harness.count("nomnom.delivery.created", None), 1);
}

#[test]
fn failed_outbox_write_records_nothing() {
    let harness = Harness::new();
    let store = TestStore::new();
    store.set_unavailable(true);

    assert!(
        harness
            .observed
            .add(&store, OutboxMessage::delivery(created()))
            .is_err()
    );

    assert_eq!(harness.count("nomnom.delivery.created", None), 0);
}

#[test]
fn committed_failure_is_logged_once_and_rolled_back_failure_is_not() {
    use crate::slices::delivery::infrastructure::failure_log::FailureLog;

    let exporter = InMemoryMetricExporter::default();
    let provider = SdkMeterProvider::builder()
        .with_reader(PeriodicReader::builder(exporter).build())
        .build();
    let observed = ObservedOutbox::new(
        DomainMetrics::new(
            &provider.meter("test"),
            &DeliveryFailureCode::CODES,
            Arc::new(|| NOW),
        ),
        TransitionMetrics::new(&provider.meter("test")),
    )
    .with_failure_log(FailureLog);
    let store = TestStore::new();
    let run = |commit: bool| {
        observed
            .run_scoped(&store, &mut || {
                observed
                    .add(&store, OutboxMessage::delivery(failed("other")))
                    .expect("outbox add");
                commit
            })
            .expect("unit of work");
    };

    let resource = opentelemetry_sdk::Resource::builder_empty().build();
    assert!(crate::pkg::telemetry::log::capture_json(&resource, || run(false)).is_empty());
    let records = crate::pkg::telemetry::log::capture_json(&resource, || run(true));

    assert_eq!(records.len(), 1);
    let record = &records[0];
    assert_eq!(record["reason"], "other");
    assert_eq!(record["detail"], "free text detail");
    assert_eq!(record["delivery_id"], "delivery-1");
    assert_eq!(record["courier_id"], "courier-1");
}

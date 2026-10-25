use std::sync::Mutex;

use super::*;
use crate::infrastructure::spicedb::{FakeGraph, courier_self, delivery_courier};

/// Business state of one relation.
#[derive(Default)]
struct FakeSource(Mutex<Vec<(String, String)>>);

impl FakeSource {
    fn with(rows: &[(&str, &str)]) -> Self {
        Self(Mutex::new(
            rows.iter()
                .map(|(resource, subject)| ((*resource).to_owned(), (*subject).to_owned()))
                .collect(),
        ))
    }
}

impl RelationSource for Arc<FakeSource> {
    fn subjects_of(&self, ids: &[String]) -> Result<HashMap<String, String>, String> {
        Ok(self
            .0
            .lock()
            .expect("rows")
            .iter()
            .filter(|(resource, _)| ids.contains(resource))
            .cloned()
            .collect())
    }

    fn page(&self, after: &str, limit: usize) -> Result<Vec<(String, String)>, String> {
        let mut rows = self.0.lock().expect("rows").clone();
        rows.sort();
        Ok(rows
            .into_iter()
            .filter(|(resource, _)| resource.as_str() > after)
            .take(limit)
            .collect())
    }

    fn record_revision(
        &self,
        _resource_id: &str,
        _subject_id: &str,
        _revision: AuthorizationRevision,
    ) -> Result<(), String> {
        Ok(())
    }
}

struct Harness {
    graph: Arc<FakeGraph>,
    source: Arc<FakeSource>,
    clock: Arc<Mutex<Instant>>,
    reconciler: Reconciler,
}

const GRACE: Duration = Duration::from_secs(600);

fn harness(graph: FakeGraph, source: FakeSource, page_size: u32) -> Harness {
    let graph = Arc::new(graph);
    let source = Arc::new(source);
    let clock = Arc::new(Mutex::new(Instant::now()));
    let now_clock = Arc::clone(&clock);
    let reconciler = Reconciler::new(
        Arc::clone(&graph) as Arc<dyn RelationshipGraph>,
        Box::new(Arc::clone(&source)),
        (DELIVERY, DELIVERY_COURIER),
        GRACE,
        page_size,
        Arc::new(move || *now_clock.lock().expect("clock")),
    );
    Harness {
        graph,
        source,
        clock,
        reconciler,
    }
}

impl Harness {
    fn advance(&self, by: Duration) {
        *self.clock.lock().expect("clock") += by;
    }

    fn pass(&mut self) -> Stats {
        self.reconciler.reconcile(&|| false).expect("pass")
    }
}

#[test]
fn missing_relationship_is_restored_immediately() {
    let mut h = harness(
        FakeGraph::default(),
        FakeSource::with(&[("delivery-1", "courier-1")]),
        500,
    );

    let stats = h.pass();

    assert_eq!(stats.missing_repaired, 1);
    assert!(
        h.graph
            .contains(&delivery_courier("delivery-1", "courier-1"))
    );
    assert_eq!(
        h.pass(),
        Stats {
            checked: 1,
            ..Stats::default()
        }
    );
}

#[test]
fn orphan_is_deleted_only_after_two_passes_grace_apart() {
    let orphan = delivery_courier("delivery-9", "courier-1");
    let mut h = harness(
        FakeGraph::with([orphan.clone()]),
        FakeSource::default(),
        500,
    );

    assert_eq!(h.pass().orphans_pending, 1);
    h.advance(GRACE - Duration::from_secs(1));
    assert_eq!(h.pass().orphans_pending, 1);
    assert!(h.graph.contains(&orphan));

    h.advance(Duration::from_secs(1));
    let stats = h.pass();

    assert_eq!(stats.orphans_deleted, 1);
    assert!(!h.graph.contains(&orphan));
}

#[test]
fn stale_subject_is_deleted_and_current_restored() {
    let stale = delivery_courier("delivery-1", "courier-old");
    let mut h = harness(
        FakeGraph::with([stale.clone()]),
        FakeSource::with(&[("delivery-1", "courier-new")]),
        500,
    );

    let first = h.pass();
    assert_eq!((first.orphans_pending, first.missing_repaired), (1, 1));
    h.advance(GRACE);
    assert_eq!(h.pass().orphans_deleted, 1);

    assert!(!h.graph.contains(&stale));
    assert!(
        h.graph
            .contains(&delivery_courier("delivery-1", "courier-new"))
    );
}

#[test]
fn candidate_is_forgotten_when_business_catches_up() {
    let relationship = delivery_courier("delivery-1", "courier-1");
    let mut h = harness(
        FakeGraph::with([relationship.clone()]),
        FakeSource::default(),
        500,
    );

    assert_eq!(h.pass().orphans_pending, 1);
    h.source
        .0
        .lock()
        .expect("rows")
        .push(("delivery-1".to_owned(), "courier-1".to_owned()));
    h.advance(GRACE);
    assert_eq!(
        h.pass(),
        Stats {
            checked: 1,
            ..Stats::default()
        }
    );
    // Orphan again: the grace starts over.
    h.source.0.lock().expect("rows").clear();
    h.advance(GRACE);
    assert_eq!(h.pass().orphans_pending, 1);
    assert!(h.graph.contains(&relationship));
}

#[test]
fn invalid_ids_are_unrepairable_and_not_written() {
    let mut h = harness(
        FakeGraph::default(),
        FakeSource::with(&[("delivery.1", "courier-1"), ("delivery-2", "courier 2")]),
        500,
    );

    let stats = h.pass();

    assert_eq!((stats.unrepairable, stats.missing_repaired), (2, 0));
    assert!(h.graph.writes().is_empty());
}

#[test]
fn pages_cover_graph_and_business() {
    let mut h = harness(
        FakeGraph::with((1..=5).map(|i| delivery_courier(&format!("delivery-{i}"), "c"))),
        FakeSource::with(&[
            ("delivery-1", "c"),
            ("delivery-2", "c"),
            ("delivery-3", "c"),
            ("delivery-6", "c"),
            ("delivery-7", "c"),
        ]),
        2,
    );

    let stats = h.pass();

    assert_eq!(stats.checked, 5);
    assert_eq!(stats.orphans_pending, 2);
    assert_eq!(stats.missing_repaired, 2);
}

#[test]
fn graph_outage_fails_the_pass_without_writes() {
    let mut h = harness(
        FakeGraph::default(),
        FakeSource::with(&[("delivery-1", "courier-1")]),
        500,
    );
    *h.graph.failure.lock().expect("failure") = Some("spicedb offline".to_owned());

    assert!(h.reconciler.reconcile(&|| false).is_err());
    assert!(h.graph.writes().is_empty());
}

#[test]
fn stopping_pass_returns_early() {
    let mut h = harness(
        FakeGraph::default(),
        FakeSource::with(&[("delivery-1", "courier-1")]),
        500,
    );

    assert!(h.reconciler.reconcile(&|| true).is_err());
    assert!(h.graph.writes().is_empty());
}

#[test]
fn object_id_grammar() {
    assert!(valid_object_id("courier-1_a/b|c=d+e"));
    assert!(!valid_object_id(""));
    assert!(!valid_object_id("courier.1"));
    assert!(!valid_object_id("courier 1"));
}

#[test]
fn courier_reconciler_reads_registered_couriers() {
    use crate::slices::courier::domains::courier::{Courier, CourierId, CourierRepository};
    let store = Arc::new(MemoryStore::new());
    CourierRepository::add(&*store, &Courier::new(CourierId::new("courier-1"))).expect("add");
    let graph = Arc::new(FakeGraph::with([courier_self("courier-ghost")]));
    let mut reconciler = Reconciler::couriers(
        Arc::clone(&graph) as Arc<dyn RelationshipGraph>,
        Arc::clone(&store) as _,
        Duration::ZERO,
        500,
        Arc::new(Instant::now),
    );

    let first = reconciler.reconcile(&|| false).expect("pass");
    let second = reconciler.reconcile(&|| false).expect("pass");

    assert_eq!((first.missing_repaired, first.orphans_pending), (1, 1));
    assert_eq!(second.orphans_deleted, 1);
    assert!(graph.contains(&courier_self("courier-1")));
    assert!(!graph.contains(&courier_self("courier-ghost")));
    // FR-447: the revision of the restoring TOUCH is stored with the courier.
    let restored = CourierRepository::get(&*store, &CourierId::new("courier-1"))
        .expect("read")
        .expect("courier");
    assert!(restored.authorization_revision().is_some());
}

/// FR-447: a restored `delivery#courier` stores its TOUCH revision on the
/// delivery; an orphan deletion changes no revision.
#[test]
fn delivery_reconciler_stores_revision_of_restored_assignment() {
    use crate::slices::courier::domains::courier::CourierId;
    use crate::slices::delivery::domains::delivery::{
        Address, Delivery, DeliveryId, DeliveryRepository, OrderId,
    };
    let store = Arc::new(MemoryStore::new());
    let mut delivery = Delivery::new(
        DeliveryId::new("delivery-1"),
        OrderId::new("order-1"),
        Address::new("Restaurant street 1").expect("pickup"),
        Address::new("Customer street 2").expect("dropoff"),
    );
    delivery
        .assign(CourierId::new("courier-1"))
        .expect("assign");
    delivery.record_authorization_revision(AuthorizationRevision::new("old"));
    DeliveryRepository::add(&*store, &delivery).expect("add");
    let graph = Arc::new(FakeGraph::with([delivery_courier(
        "delivery-9",
        "courier-1",
    )]));
    let mut reconciler = Reconciler::deliveries(
        Arc::clone(&graph) as Arc<dyn RelationshipGraph>,
        Arc::clone(&store) as _,
        Duration::ZERO,
        500,
        Arc::new(Instant::now),
    );

    let first = reconciler.reconcile(&|| false).expect("pass");

    assert_eq!(first.missing_repaired, 1);
    let restored = DeliveryRepository::get(&*store, &DeliveryId::new("delivery-1"))
        .expect("read")
        .expect("delivery");
    assert_eq!(
        restored.authorization_revision(),
        Some(&AuthorizationRevision::new("zed-1"))
    );
    reconciler.reconcile(&|| false).expect("orphan pass");
    assert!(!graph.contains(&delivery_courier("delivery-9", "courier-1")));
    let after_delete = DeliveryRepository::get(&*store, &DeliveryId::new("delivery-1"))
        .expect("read")
        .expect("delivery");
    assert_eq!(
        after_delete.authorization_revision(),
        Some(&AuthorizationRevision::new("zed-1"))
    );
}

#[tokio::test]
async fn loop_stops_on_shutdown() {
    let (stop_tx, stop_rx) = watch::channel(false);
    let task = tokio::spawn(run(
        Vec::new(),
        Schedule {
            first_pass: Duration::from_millis(10),
            interval: Duration::from_secs(3600),
        },
        stop_rx,
    ));
    tokio::time::sleep(Duration::from_millis(50)).await;
    stop_tx.send(true).expect("stop");

    tokio::time::timeout(Duration::from_secs(1), task)
        .await
        .expect("stopped in time")
        .expect("join");
}

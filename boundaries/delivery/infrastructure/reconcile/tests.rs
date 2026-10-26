use std::sync::Mutex;

use super::*;
use crate::infrastructure::repository::test_support::TestStore;
use crate::infrastructure::spicedb::{FakeGraph, courier_self, delivery_courier};
use crate::pkg::persistence::AuthorizationRevision;

/// Business state of one relation: `(resource, subject, row version)`.
#[derive(Default)]
struct FakeSource(Mutex<Vec<SourceRow>>);

impl FakeSource {
    /// Rows at version 1.
    fn with(rows: &[(&str, &str)]) -> Self {
        Self(Mutex::new(
            rows.iter()
                .map(|(resource, subject)| ((*resource).to_owned(), (*subject).to_owned(), 1))
                .collect(),
        ))
    }

    /// Assigns `subject` to `resource` at `version`, replacing its row.
    fn assign(&self, resource: &str, subject: &str, version: u64) {
        let mut rows = self.0.lock().expect("rows");
        rows.retain(|(id, _, _)| id != resource);
        rows.push((resource.to_owned(), subject.to_owned(), version));
    }
}

impl RelationSource for Arc<FakeSource> {
    fn subjects_of(&self, ids: &[String]) -> Result<HashMap<String, String>, String> {
        Ok(self
            .0
            .lock()
            .expect("rows")
            .iter()
            .filter(|(resource, _, _)| ids.contains(resource))
            .map(|(resource, subject, _)| (resource.clone(), subject.clone()))
            .collect())
    }

    fn page(&self, after: &str, limit: usize) -> Result<Vec<SourceRow>, String> {
        let mut rows = self.0.lock().expect("rows").clone();
        rows.sort();
        Ok(rows
            .into_iter()
            .filter(|(resource, _, _)| resource.as_str() > after)
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
fn missing_relationship_is_restored_after_two_passes_grace_apart() {
    let mut h = harness(
        FakeGraph::default(),
        FakeSource::with(&[("delivery-1", "courier-1")]),
        500,
    );

    assert_eq!(h.pass().missing_pending, 1);
    h.advance(GRACE - Duration::from_secs(1));
    assert_eq!(h.pass().missing_pending, 1);
    assert!(
        h.graph.writes().is_empty(),
        "inside grace nothing is restored"
    );

    h.advance(Duration::from_secs(1));
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

/// FR-563, AC-668: Reassign Courier deleted `delivery#courier` of courier-1 before
/// its commit; the pass reads the graph after the DELETE and business state
/// before the commit. The relationship is not restored, and once the
/// unassignment committed the candidate is forgotten.
#[test]
fn relationship_deleted_by_an_uncommitted_writer_is_not_resurrected() {
    let released = delivery_courier("delivery-1", "courier-1");
    let mut h = harness(
        FakeGraph::default(),
        FakeSource::with(&[("delivery-1", "courier-1")]),
        500,
    );

    assert_eq!(h.pass().missing_pending, 1);
    assert!(!h.graph.contains(&released));
    // The unassignment commits: business state no longer names courier-1.
    h.source.0.lock().expect("rows").clear();
    h.advance(GRACE);
    assert_eq!(
        h.pass(),
        Stats::default(),
        "the candidate is forgotten without a write"
    );
    assert!(h.graph.writes().is_empty());

    // Assigned again later: the grace starts over.
    h.source.assign("delivery-1", "courier-1", 3);
    h.advance(GRACE);
    assert_eq!(h.pass().missing_pending, 1);
    assert!(!h.graph.contains(&released));
}

/// FR-563, AC-760: between the two passes the courier was re-assigned and revoked
/// again (two commits, the subject is the same on both passes). Each commit
/// bumped the row version, so the second pass does not restore: the
/// relationship it sees missing may come from the newer writer's DELETE.
#[test]
fn reassigned_and_revoked_between_passes_is_not_restored() {
    let released = delivery_courier("delivery-1", "courier-1");
    let mut h = harness(
        FakeGraph::default(),
        FakeSource::with(&[("delivery-1", "courier-1")]),
        500,
    );

    assert_eq!(h.pass().missing_pending, 1);
    // Reassign Courier away and back to courier-1, then a new revoke deletes
    // the relationship before its commit: same subject, newer version.
    h.source.assign("delivery-1", "courier-1", 4);
    h.advance(GRACE);
    let second = h.pass();
    assert_eq!((second.missing_pending, second.missing_repaired), (1, 0));
    assert!(h.graph.writes().is_empty(), "not restored on a new version");

    // Unchanged on two passes at the new version: restored after a full grace.
    h.advance(GRACE - Duration::from_secs(1));
    assert_eq!(h.pass().missing_pending, 1);
    assert!(!h.graph.contains(&released));
    h.advance(Duration::from_secs(1));
    assert_eq!(h.pass().missing_repaired, 1);
    assert!(h.graph.contains(&released));
}

#[test]
fn missing_candidate_is_forgotten_when_the_graph_catches_up() {
    let relationship = delivery_courier("delivery-1", "courier-1");
    let mut h = harness(
        FakeGraph::default(),
        FakeSource::with(&[("delivery-1", "courier-1")]),
        500,
    );

    assert_eq!(h.pass().missing_pending, 1);
    // The writer's TOUCH lands (Accept Delivery Offer in flight).
    h.graph
        .write(RelationshipOperation::Touch, &relationship)
        .expect("touch");
    let writes = h.graph.writes().len();
    h.advance(GRACE);
    assert_eq!(
        h.pass(),
        Stats {
            checked: 1,
            ..Stats::default()
        }
    );
    assert_eq!(h.graph.writes().len(), writes);
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
    assert_eq!((first.orphans_pending, first.missing_pending), (1, 1));
    h.advance(GRACE);
    let second = h.pass();
    assert_eq!((second.orphans_deleted, second.missing_repaired), (1, 1));

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
    h.source.assign("delivery-1", "courier-1", 3);
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
    assert_eq!(stats.missing_pending, 2);
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
    use crate::slices::courier::domains::courier::{
        Courier, CourierId, CourierName, CourierRepository,
    };
    let store = Arc::new(TestStore::new());
    CourierRepository::add(
        &*store,
        &Courier::new(
            CourierId::new("courier-1"),
            CourierName::new("Timur").expect("courier name"),
        ),
    )
    .expect("add");
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

    assert_eq!((first.missing_pending, first.orphans_pending), (1, 1));
    assert_eq!((second.missing_repaired, second.orphans_deleted), (1, 1));
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
        Delivery, DeliveryId, DeliveryRepository, OrderId,
    };
    let store = Arc::new(TestStore::new());
    let mut delivery = Delivery::new(DeliveryId::new("delivery-1"), OrderId::new("order-1"));
    delivery
        .assign(CourierId::new("courier-1"), None)
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

    assert_eq!(
        reconciler
            .reconcile(&|| false)
            .expect("pass")
            .missing_pending,
        1
    );
    let first = reconciler.reconcile(&|| false).expect("pass");

    // zed-1 is the DELETE of the orphan, zed-2 the restoring TOUCH.
    assert_eq!(first.missing_repaired, 1);
    let restored = DeliveryRepository::get(&*store, &DeliveryId::new("delivery-1"))
        .expect("read")
        .expect("delivery");
    assert_eq!(
        restored.authorization_revision(),
        Some(&AuthorizationRevision::new("zed-2"))
    );
    reconciler.reconcile(&|| false).expect("orphan pass");
    assert!(!graph.contains(&delivery_courier("delivery-9", "courier-1")));
    let after_delete = DeliveryRepository::get(&*store, &DeliveryId::new("delivery-1"))
        .expect("read")
        .expect("delivery");
    assert_eq!(
        after_delete.authorization_revision(),
        Some(&AuthorizationRevision::new("zed-2"))
    );
}

/// FR-563, AC-693: a delivered or failed delivery keeps `courier_id`, but
/// its `delivery#courier` is stale: a remaining one is deleted after grace and
/// a missing one is never restored. The active assignment is kept.
#[test]
fn closed_delivery_relationship_is_stale_and_never_restored() {
    use crate::slices::courier::domains::courier::CourierId;
    use crate::slices::delivery::domains::delivery::{
        Delivery, DeliveryFailureReason, DeliveryId, DeliveryRepository, FailureReporter, OrderId,
    };
    let store = Arc::new(TestStore::new());
    let delivery = |id: &str, courier: &str| {
        let mut delivery =
            Delivery::new(DeliveryId::new(id), OrderId::new(format!("order-of-{id}")));
        delivery
            .assign(CourierId::new(courier), None)
            .expect("assign");
        delivery
    };
    let mut delivered = delivery("delivered", "courier-1");
    delivered
        .confirm_pickup(&CourierId::new("courier-1"), None)
        .expect("pickup");
    delivered
        .complete(&CourierId::new("courier-1"))
        .expect("complete");
    let mut failed = delivery("failed", "courier-2");
    failed
        .fail(
            DeliveryFailureReason::new("customer_unreachable", None).expect("reason"),
            &FailureReporter::Dispatcher,
        )
        .expect("fail");
    for closed in [&delivered, &failed, &delivery("active", "courier-3")] {
        DeliveryRepository::add(&*store, closed).expect("add");
    }
    // The link of the delivered delivery remained; the failed one has none.
    let remained = delivery_courier("delivered", "courier-1");
    let active = delivery_courier("active", "courier-3");
    let graph = Arc::new(FakeGraph::with([remained.clone(), active.clone()]));
    let clock = Arc::new(Mutex::new(Instant::now()));
    let now_clock = Arc::clone(&clock);
    let mut reconciler = Reconciler::deliveries(
        Arc::clone(&graph) as Arc<dyn RelationshipGraph>,
        Arc::clone(&store) as _,
        GRACE,
        500,
        Arc::new(move || *now_clock.lock().expect("clock")),
    );

    let first = reconciler.reconcile(&|| false).expect("pass");
    assert_eq!(first.orphans_pending, 1);
    assert_eq!(
        first.missing_pending, 0,
        "a closed delivery has nothing missing"
    );
    *clock.lock().expect("clock") += GRACE;
    let second = reconciler.reconcile(&|| false).expect("pass");

    assert_eq!(second.orphans_deleted, 1);
    assert_eq!(second.missing_repaired, 0);
    assert!(!graph.contains(&remained));
    assert!(!graph.contains(&delivery_courier("failed", "courier-2")));
    assert!(graph.contains(&active));
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

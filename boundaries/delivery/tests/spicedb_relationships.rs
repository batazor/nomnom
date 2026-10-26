//! Integration-тесты записи relationships и reconciliation (FR-444, FR-445,
//! AC-477, AC-690, AC-691) против SpiceDB из testcontainers с `authorization/bootstrap.yaml`.
//!
//! Проверки прав идут через SpiceDB-адаптер с revisions из хранилища
//! (FR-447, AC-483): ресурс с сохранённой revision проверяется
//! `at_least_as_fresh`, поэтому собственная запись видна сразу, без повторов и
//! без принудительной full consistency. Повтор в пределах окна квантования
//! остаётся только для relationships без revision (orphan, записанные мимо
//! use case), которые проверяются с `minimize_latency` (FR-448).

mod support;

use std::sync::Arc;
use std::sync::atomic::{AtomicU64, Ordering};
use std::time::{Duration, Instant, SystemTime, UNIX_EPOCH};

use delivery::infrastructure::memory::{ManualClock, StaticAuthorization, StaticCourierLocator};
use delivery::infrastructure::reconcile::Reconciler;
use delivery::infrastructure::repository::PostgresStore;
use delivery::infrastructure::spicedb::{
    Relationship, RelationshipGraph, RelationshipOperation, RelationshipPage, RepositoryRevisions,
    SpiceDbAuthorization, SpiceDbClient, SpiceDbRelationships, courier_self, delivery_courier,
};
use delivery::pkg::application::{AuthorizationError, Subject};
use delivery::pkg::config::DatabaseConfig;
use delivery::pkg::persistence::AuthorizationRevision;
use delivery::slices::courier::applications::authorization::{
    CourierAuthorization, CourierPermission, CourierResource,
};
use delivery::slices::courier::applications::register_courier;
use delivery::slices::courier::domains::courier::{Courier, CourierId, CourierRepository};
use delivery::slices::delivery::applications::authorization::{
    DeliveryAuthorization, DeliveryPermission, DeliveryResource,
};
use delivery::slices::delivery::applications::courier_locator::CourierCandidate;
use delivery::slices::delivery::applications::policy::AssignmentPolicy;
use delivery::slices::delivery::applications::{
    accept_delivery_offer, cancel_delivery, confirm_delivery, confirm_pickup, reassign_courier,
    report_delivery_failure,
};
use delivery::slices::delivery::domains::delivery::{
    Address, Delivery, DeliveryId, DeliveryOffer, DeliveryOfferId, DeliveryOfferRepository,
    DeliveryRepository, OrderId,
};

const NOW: i64 = 1_000;
/// Больше окна квантования SpiceDB по умолчанию (5s).
const EVENTUALLY: Duration = Duration::from_secs(15);

fn live() -> SpiceDbClient {
    SpiceDbClient::connect(&support::spicedb(Duration::from_secs(2))).expect("valid config")
}

/// Уникальный id без точек: тесты делят один SpiceDB и одну базу. Счётчик
/// различает id параллельных потоков в пределах одного тика часов.
fn unique(prefix: &str) -> String {
    static NEXT: AtomicU64 = AtomicU64::new(0);
    let nanos = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .expect("clock")
        .as_nanos();
    let sequence = NEXT.fetch_add(1, Ordering::Relaxed);
    format!("it-{prefix}-{}-{nanos}-{sequence}", std::process::id())
}

fn user(id: &str) -> Subject {
    Subject::User(id.to_owned())
}

/// Адаптер с revisions из `store`: checks ресурсов с revision —
/// `at_least_as_fresh`, остальные — `minimize_latency`.
fn adapter(client: &SpiceDbClient, store: &Arc<PostgresStore>) -> SpiceDbAuthorization {
    SpiceDbAuthorization::new(Box::new(client.clone()))
        .with_revisions(Arc::new(RepositoryRevisions(Arc::clone(store))))
}

/// Один check, без повторов: решение должно быть верным сразу.
fn immediately(expected: bool, outcome: Result<(), AuthorizationError>) {
    match (&outcome, expected) {
        (Ok(()), true) | (Err(AuthorizationError::Denied), false) => {}
        _ => panic!("expected allowed = {expected} immediately, got {outcome:?}"),
    }
}

/// Ждёт, пока check вернёт `expected`: только для relationships без revision.
fn eventually(expected: bool, check: impl Fn() -> Result<(), AuthorizationError>) {
    let started = Instant::now();
    loop {
        let outcome = check();
        match (&outcome, expected) {
            (Ok(()), true) | (Err(AuthorizationError::Denied), false) => return,
            (Err(AuthorizationError::Unavailable(reason)), _) => panic!("spicedb: {reason}"),
            _ if started.elapsed() > EVENTUALLY => {
                panic!("expected allowed = {expected}, got {outcome:?}")
            }
            _ => std::thread::sleep(Duration::from_millis(250)),
        }
    }
}

fn courier_act(
    authorization: &SpiceDbAuthorization,
    courier: &str,
) -> Result<(), AuthorizationError> {
    CourierAuthorization::check(
        authorization,
        &user(courier),
        CourierPermission::SetCourierAvailability,
        &CourierResource::Courier(CourierId::new(courier)),
    )
}

fn act_as_courier(
    authorization: &SpiceDbAuthorization,
    delivery: &str,
    courier: &str,
) -> Result<(), AuthorizationError> {
    DeliveryAuthorization::check(
        authorization,
        &user(courier),
        DeliveryPermission::ConfirmPickup,
        &DeliveryResource::Delivery(DeliveryId::new(delivery)),
    )
}

/// The delivery carries a stored revision, so the check is decided at once.
fn acts_as_courier(world: &Assigned, client: &SpiceDbClient, allowed: bool) {
    let revision =
        DeliveryRepository::get(&*world.store, &DeliveryId::new(world.delivery.as_str()))
            .expect("read delivery")
            .expect("delivery")
            .authorization_revision()
            .cloned();
    assert!(revision.is_some(), "delivery has no authorization revision");
    immediately(
        allowed,
        act_as_courier(
            &adapter(client, &world.store),
            &world.delivery,
            &world.courier,
        ),
    );
}

/// Хранилище с курьером и доставкой, принятой им через Accept Delivery Offer.
struct Assigned {
    store: Arc<PostgresStore>,
    writer: SpiceDbRelationships,
    delivery: String,
    order: String,
    courier: String,
}

fn assigned(client: &SpiceDbClient, tag: &str) -> Assigned {
    assigned_in(client, tag, &support::database())
}

fn assigned_in(client: &SpiceDbClient, tag: &str, database: &DatabaseConfig) -> Assigned {
    let store = Arc::new(PostgresStore::connect(database).expect("connect"));
    let writer = SpiceDbRelationships::new(Arc::new(client.clone()));
    let courier = unique(&format!("{tag}-courier"));
    let delivery = unique(&format!("{tag}-delivery"));
    let order = unique(&format!("{tag}-order"));
    let offer = unique("offer");
    CourierRepository::add(&*store, &Courier::new(CourierId::new(courier.as_str())))
        .expect("seed courier");
    DeliveryRepository::add(
        &*store,
        &Delivery::new(
            DeliveryId::new(delivery.as_str()),
            OrderId::new(order.as_str()),
            Address::new("Restaurant street 1").expect("pickup"),
        ),
    )
    .expect("seed delivery");
    DeliveryOfferRepository::add(
        &*store,
        &DeliveryOffer::new(
            DeliveryOfferId::new(offer.as_str()),
            DeliveryId::new(delivery.as_str()),
            CourierId::new(courier.as_str()),
            NOW,
            NOW + 60,
        )
        .expect("offer"),
    )
    .expect("seed offer");
    let locator = StaticCourierLocator::new(vec![CourierCandidate {
        courier_id: CourierId::new(courier.as_str()),
        within_delivery_zone: true,
        distance_to_pickup_meters: 100,
        active_assignments: 0,
    }]);
    let authorization = StaticAuthorization::allow_all();
    accept_delivery_offer::Handler {
        authorization: &authorization,
        unit_of_work: &*store,
        offers: &*store,
        offers_of: &*store,
        deliveries: &*store,
        couriers: &*store,
        outbox: &*store,
        locator: &locator,
        clock: &ManualClock::new(NOW),
        policy: AssignmentPolicy {
            maximum_distance_meters: 5_000,
            maximum_active_assignments: 1,
            offer_ttl_seconds: 60,
        },
        relationships: &writer,
    }
    .execute(
        &user(&courier),
        accept_delivery_offer::Input {
            offer_id: offer,
            courier_id: courier.clone(),
            expected_pickup_at_epoch_seconds: None,
        },
    )
    .expect("offer accepted");
    Assigned {
        store,
        writer,
        delivery,
        order,
        courier,
    }
}

/// FR-447: the courier acts for itself right after registration.
#[test]
fn registered_courier_acts_for_itself() {
    let client = live();
    let store = Arc::new(PostgresStore::connect(&support::database()).expect("connect"));
    let writer = SpiceDbRelationships::new(Arc::new(client.clone()));
    let courier = unique("courier");

    register_courier::Handler {
        authorization: &StaticAuthorization::allow_all(),
        couriers: &*store,
        relationships: &writer,
    }
    .execute(
        &user("operator-1"),
        register_courier::Input {
            courier_id: courier.clone(),
        },
    )
    .expect("courier registered");

    let revision: Option<AuthorizationRevision> =
        CourierRepository::get(&*store, &CourierId::new(courier.as_str()))
            .expect("read courier")
            .expect("courier")
            .authorization_revision()
            .cloned();
    assert!(revision.is_some(), "courier has no authorization revision");
    immediately(true, courier_act(&adapter(&client, &store), &courier));
}

/// FR-447: the courier acts on the delivery right after Accept Delivery Offer.
#[test]
fn accepted_offer_grants_act_as_courier() {
    let client = live();

    let world = assigned(&client, "t");

    acts_as_courier(&world, &client, true);
}

/// AC-477, AC-483: denied right after Reassign Courier.
#[test]
fn reassigned_courier_loses_act_as_courier() {
    let client = live();
    let world = assigned(&client, "t");
    acts_as_courier(&world, &client, true);

    reassign_courier::Handler {
        authorization: &StaticAuthorization::allow_all(),
        unit_of_work: &*world.store,
        deliveries: &*world.store,
        couriers: &*world.store,
        outbox: &*world.store,
        relationships: &world.writer,
    }
    .execute(
        &user("dispatcher-1"),
        reassign_courier::Input {
            delivery_id: world.delivery.clone(),
        },
    )
    .expect("reassigned");

    acts_as_courier(&world, &client, false);
}

/// FR-447: denied right after Cancel Delivery.
#[test]
fn cancelled_delivery_courier_loses_act_as_courier() {
    let client = live();
    let world = assigned(&client, "t");
    acts_as_courier(&world, &client, true);

    cancel_delivery::Handler {
        authorization: &StaticAuthorization::allow_all(),
        unit_of_work: &*world.store,
        deliveries: &*world.store,
        couriers: &*world.store,
        outbox: &*world.store,
        relationships: &world.writer,
    }
    .execute(
        &Subject::Service("orders-worker".to_owned()),
        cancel_delivery::Input {
            order_id: world.order.clone(),
            reason: String::new(),
        },
    )
    .expect("cancelled");

    acts_as_courier(&world, &client, false);
}

fn pick_up(world: &Assigned) {
    confirm_pickup::Handler {
        authorization: &StaticAuthorization::allow_all(),
        unit_of_work: &*world.store,
        deliveries: &*world.store,
        outbox: &*world.store,
        clock: &ManualClock::new(NOW),
    }
    .execute(
        &user(&world.courier),
        confirm_pickup::Input {
            delivery_id: world.delivery.clone(),
            courier_id: world.courier.clone(),
            expected_delivered_at_epoch_seconds: None,
        },
    )
    .expect("picked up");
}

/// AC-690: denied right after Confirm Delivery; `courier_id` stays.
#[test]
fn delivered_courier_loses_act_as_courier() {
    let client = live();
    let world = assigned(&client, "t");
    pick_up(&world);
    acts_as_courier(&world, &client, true);

    confirm_delivery::Handler {
        authorization: &StaticAuthorization::allow_all(),
        unit_of_work: &*world.store,
        deliveries: &*world.store,
        couriers: &*world.store,
        outbox: &*world.store,
        relationships: &world.writer,
    }
    .execute(
        &user(&world.courier),
        confirm_delivery::Input {
            delivery_id: world.delivery.clone(),
            courier_id: world.courier.clone(),
        },
    )
    .expect("delivered");

    acts_as_courier(&world, &client, false);
    let delivery =
        DeliveryRepository::get(&*world.store, &DeliveryId::new(world.delivery.as_str()))
            .expect("read delivery")
            .expect("delivery");
    assert_eq!(
        delivery.courier_id(),
        Some(&CourierId::new(world.courier.as_str()))
    );
}

/// AC-691: denied right after a dispatcher reports the failure.
#[test]
fn failed_delivery_courier_loses_act_as_courier() {
    let client = live();
    let world = assigned(&client, "t");
    acts_as_courier(&world, &client, true);

    report_delivery_failure::Handler {
        authorization: &StaticAuthorization::allow_all(),
        unit_of_work: &*world.store,
        deliveries: &*world.store,
        couriers: &*world.store,
        outbox: &*world.store,
        relationships: &world.writer,
    }
    .execute(
        &user("dispatcher-1"),
        report_delivery_failure::Input {
            delivery_id: world.delivery.clone(),
            reporter: report_delivery_failure::Reporter::Dispatcher,
            reason: "customer_unreachable".to_owned(),
            detail: None,
        },
    )
    .expect("failed");

    acts_as_courier(&world, &client, false);
}

/// AC-694: the platform operator reports the failure of a delivery it is
/// not assigned to; a user without `operator` is denied.
#[test]
fn operator_dispatches_delivery_failure() {
    let client = live();
    let operator = unique("operator");
    client
        .write(
            RelationshipOperation::Touch,
            &Relationship::new("platform", "nomnom", "operator", "user", &operator),
        )
        .expect("grant operator");
    let authorization = adapter(
        &client,
        &Arc::new(PostgresStore::connect(&support::database()).expect("connect")),
    );
    let report = |subject: &str| {
        DeliveryAuthorization::check(
            &authorization,
            &user(subject),
            DeliveryPermission::ReportDeliveryFailure,
            &DeliveryResource::Delivery(DeliveryId::new(unique("delivery"))),
        )
    };

    // `platform:nomnom` has no stored revision: minimize_latency.
    eventually(true, || report(&operator));
    immediately(false, report(&unique("stranger")));
}

/// Graph limited to relationships created by this test: the shared SpiceDB
/// holds relationships of parallel tests, which the reconciler must not touch.
struct ScopedGraph {
    inner: SpiceDbClient,
    marker: String,
}

impl RelationshipGraph for ScopedGraph {
    fn write(
        &self,
        operation: RelationshipOperation,
        relationship: &Relationship,
    ) -> Result<AuthorizationRevision, String> {
        assert!(
            relationship.resource_id.contains(&self.marker),
            "write outside the test scope: {relationship}"
        );
        self.inner.write(operation, relationship)
    }

    /// Returns every scoped relationship in one page.
    fn read_page(
        &self,
        resource_type: &str,
        relation: &str,
        _cursor: Option<&str>,
        _limit: u32,
    ) -> Result<RelationshipPage, String> {
        let mut scoped = RelationshipPage::default();
        let mut cursor: Option<String> = None;
        loop {
            let page = self
                .inner
                .read_page(resource_type, relation, cursor.as_deref(), 1000)?;
            let exhausted = page.relationships.len() < 1000 || page.cursor.is_none();
            scoped.relationships.extend(
                page.relationships
                    .into_iter()
                    .filter(|relationship| relationship.resource_id.contains(&self.marker)),
            );
            if exhausted {
                return Ok(scoped);
            }
            cursor = page.cursor;
        }
    }
}

/// FR-445, FR-563, AC-668: reconciliation restores a lost relationship and deletes an
/// orphan only after the grace period on a later pass.
#[test]
fn reconciler_restores_lost_and_deletes_orphan_after_grace() {
    let client = live();
    // Every resource id of this test carries its own marker.
    let marker = unique("rc");
    // Reconciliation scans every stored delivery: parallel tests must not
    // share its database.
    let world = assigned_in(&client, &marker, &support::own_database());
    let graph: Arc<dyn RelationshipGraph> = Arc::new(ScopedGraph {
        inner: client.clone(),
        marker: marker.clone(),
    });
    let store = Arc::clone(&world.store);
    let lost = delivery_courier(&world.delivery, &world.courier);
    let orphan_delivery = unique(&format!("{marker}-orphan"));
    let orphan = delivery_courier(&orphan_delivery, &world.courier);
    let ghost = unique(&format!("{marker}-ghost"));
    graph
        .write(RelationshipOperation::Delete, &lost)
        .expect("lose relationship");
    graph
        .write(RelationshipOperation::Touch, &orphan)
        .expect("write orphan");
    graph
        .write(RelationshipOperation::Touch, &courier_self(&ghost))
        .expect("write orphan courier");

    let clock = Arc::new(std::sync::Mutex::new(Instant::now()));
    let now = {
        let clock = Arc::clone(&clock);
        Arc::new(move || *clock.lock().expect("clock"))
    };
    let grace = Duration::from_secs(600);
    let mut deliveries = Reconciler::deliveries(
        Arc::clone(&graph),
        Arc::clone(&store) as _,
        grace,
        500,
        now.clone(),
    );
    let mut couriers =
        Reconciler::couriers(Arc::clone(&graph), Arc::clone(&store) as _, grace, 500, now);
    // Orphans carry no revision: they are checked with `minimize_latency`.
    let unrevisioned = adapter(&client, &store);

    let first = deliveries.reconcile(&|| false).expect("first pass");
    couriers.reconcile(&|| false).expect("first courier pass");
    assert!(first.missing_pending >= 1, "{first:?}");
    assert_eq!(first.missing_repaired, 0, "{first:?}");
    assert!(first.orphans_pending >= 1, "{first:?}");

    // Second pass before the grace: the orphan stays.
    *clock.lock().expect("clock") += grace - Duration::from_secs(1);
    deliveries.reconcile(&|| false).expect("second pass");
    eventually(true, || {
        act_as_courier(&unrevisioned, &orphan_delivery, &world.courier)
    });

    *clock.lock().expect("clock") += Duration::from_secs(1);
    let third = deliveries.reconcile(&|| false).expect("third pass");
    let courier_pass = couriers.reconcile(&|| false).expect("courier pass");
    assert!(third.missing_repaired >= 1, "{third:?}");
    assert!(third.orphans_deleted >= 1, "{third:?}");
    assert!(courier_pass.orphans_deleted >= 1, "{courier_pass:?}");
    eventually(false, || {
        act_as_courier(&unrevisioned, &orphan_delivery, &world.courier)
    });
    eventually(false, || courier_act(&unrevisioned, &ghost));
    // The restoring TOUCH stored its revision: the check sees it at once.
    acts_as_courier(&world, &client, true);
}

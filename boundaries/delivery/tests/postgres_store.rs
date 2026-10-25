//! Integration-тесты PostgreSQL-хранилища (ADR 0001 контекста) против
//! настоящего PostgreSQL. Запускаются только при заданном `DATABASE_URL`:
//!
//! `DATABASE_URL=postgres://postgres:postgres@localhost:5432/postgres cargo test --test postgres_store`
//!
//! Без переменной тесты завершаются сразу. Каждый тест использует уникальные
//! идентификаторы, поэтому база может быть общей.

use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::{Arc, Barrier};
use std::time::{SystemTime, UNIX_EPOCH};

use delivery::infrastructure::locator::CourierPositions;
use delivery::infrastructure::postgres::{
    OutboxMapper, OutboxRecord, PostgresStore, event_id, migrate,
};
use delivery::infrastructure::reconcile::ReconciliationStore;
use delivery::pkg::application::{UnitOfWork, UnitOfWorkError};
use delivery::pkg::config::DatabaseConfig;
use delivery::pkg::persistence::{AuthorizationRevision, RepositoryError};
use delivery::slices::courier::applications::update_courier_location::{
    CourierLocationStore, CourierPosition,
};
use delivery::slices::courier::domains::courier::{
    Courier, CourierId, CourierRepository, CourierStatus, Location,
};
use delivery::slices::delivery::applications::dispatch_deliveries::{
    DeliveryPosition, WaitingDeliveries,
};
use delivery::slices::delivery::applications::get_courier_current_delivery::CourierCurrentDeliveryReadModel;
use delivery::slices::delivery::applications::list_available_delivery_offers::PendingDeliveryOffersReadModel;
use delivery::slices::delivery::applications::outbox::{DeliveryOutbox, OutboxMessage};
use delivery::slices::delivery::domains::delivery::{
    Address, Delivery, DeliveryFailureCode, DeliveryFailureReason, DeliveryId, DeliveryOffer,
    DeliveryOfferId, DeliveryOfferRepository, DeliveryRepository, DomainEvent, FailureReporter,
    OfferState, OrderId, State,
};

const DSN_ENV: &str = "DATABASE_URL";

fn config() -> Option<DatabaseConfig> {
    let dsn = std::env::var(DSN_ENV).ok().filter(|dsn| !dsn.is_empty())?;
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
            .with_outbox_mapper(Arc::new(TestMapper)),
    )
}

/// Publishes only CourierAssignedToDelivery; the payload is the courier id.
struct TestMapper;

impl OutboxMapper for TestMapper {
    fn map(&self, message: &OutboxMessage) -> Option<OutboxRecord> {
        match &message.event {
            DomainEvent::CourierAssignedToDelivery { courier_id, .. } => Some(OutboxRecord {
                event_type: "nomnom.delivery.v1.CourierAssignedToDelivery".to_owned(),
                payload: courier_id.as_str().as_bytes().to_vec(),
                metadata: serde_json::json!({"traceparent": "00-test"}),
            }),
            _ => None,
        }
    }
}

fn unique(prefix: &str) -> String {
    static NEXT: AtomicUsize = AtomicUsize::new(0);
    let nanos = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .expect("clock")
        .as_nanos();
    format!("{prefix}-{nanos}-{}", NEXT.fetch_add(1, Ordering::Relaxed))
}

fn new_delivery(id: &str) -> Delivery {
    Delivery::new(
        DeliveryId::new(id),
        OrderId::new(unique("order")),
        Address::new("Restaurant street 1").expect("pickup"),
        Address::new("Customer street 2").expect("dropoff"),
    )
}

fn assigned(delivery: &Delivery, courier: &str) -> Delivery {
    Delivery::restore(
        delivery.id().clone(),
        delivery.order_id().clone(),
        Some(CourierId::new(courier)),
        delivery.pickup_address().clone(),
        delivery.dropoff_address().clone(),
        State::Assigned,
        None,
    )
    .expect("assigned")
    .with_authorization_revision(delivery.authorization_revision().cloned())
    .with_version(delivery.version())
}

/// `(event_id, event_type, aggregate_version, payload, metadata)` of the delivery.
type OutboxRow = (String, String, i64, Vec<u8>, String);

fn outbox_rows(aggregate_id: &str) -> Vec<OutboxRow> {
    let config = config().expect("url");
    let id = aggregate_id.to_owned();
    tokio::runtime::Builder::new_current_thread()
        .enable_all()
        .build()
        .expect("runtime")
        .block_on(async move {
            let mut connection = <sqlx::PgConnection as sqlx::Connection>::connect(&config.url)
                .await
                .expect("connect");
            sqlx::query_as(
                "SELECT event_id::text, event_type, aggregate_version, payload, metadata::text
                 FROM delivery_outbox WHERE aggregate_id = $1 ORDER BY seq",
            )
            .bind(id)
            .fetch_all(&mut connection)
            .await
            .expect("outbox")
        })
}

/// Runs one statement with a single text parameter (raw writes of old rows).
fn execute(statement: &'static str, parameter: &str) {
    let config = config().expect("url");
    let parameter = parameter.to_owned();
    tokio::runtime::Builder::new_current_thread()
        .enable_all()
        .build()
        .expect("runtime")
        .block_on(async move {
            let mut connection = <sqlx::PgConnection as sqlx::Connection>::connect(&config.url)
                .await
                .expect("connect");
            sqlx::query(statement)
                .bind(parameter)
                .execute(&mut connection)
                .await
                .expect("execute");
        });
}

#[derive(Debug, PartialEq)]
enum TestError {
    Repository(RepositoryError),
    Abort,
    Transaction(UnitOfWorkError),
}

impl From<UnitOfWorkError> for TestError {
    fn from(error: UnitOfWorkError) -> Self {
        Self::Transaction(error)
    }
}

impl From<RepositoryError> for TestError {
    fn from(error: RepositoryError) -> Self {
        Self::Repository(error)
    }
}

#[test]
fn migrations_are_idempotent() {
    let Some(config) = config() else { return };
    migrate(&config).expect("first run");
    assert_eq!(migrate(&config).expect("second run"), Vec::<i64>::new());
}

#[test]
fn empty_dsn_fails_fast() {
    let Err(error) = PostgresStore::connect(&DatabaseConfig::default()) else {
        panic!("an empty DSN must fail");
    };
    assert!(error.contains("DATABASE__URL"));
}

#[test]
fn courier_round_trip_with_optimistic_versions() {
    let Some(store) = store() else { return };
    let id = unique("courier");
    let mut courier = Courier::new(CourierId::new(&id));
    courier.record_authorization_revision(AuthorizationRevision::new("zed-1"));
    CourierRepository::add(&store, &courier).expect("add");
    assert_eq!(
        CourierRepository::add(&store, &courier),
        Err(RepositoryError::Conflict)
    );

    let loaded = CourierRepository::get(&store, courier.id())
        .expect("get")
        .expect("courier");
    assert_eq!(loaded, courier);
    assert_eq!(loaded.version(), 0);

    let busy = Courier::restore(CourierId::new(&id), CourierStatus::Busy)
        .with_authorization_revision(loaded.authorization_revision().cloned())
        .with_version(loaded.version());
    CourierRepository::save(&store, &busy).expect("save");
    let reloaded = CourierRepository::get(&store, courier.id())
        .expect("get")
        .expect("courier");
    assert_eq!(reloaded.status(), CourierStatus::Busy);
    assert_eq!(reloaded.version(), 1);
    assert_eq!(
        reloaded.authorization_revision(),
        Some(&AuthorizationRevision::new("zed-1"))
    );

    // `loaded` is stale now.
    assert_eq!(
        CourierRepository::save(&store, &loaded),
        Err(RepositoryError::ConcurrentModification)
    );
    assert_eq!(
        CourierRepository::save(&store, &Courier::new(CourierId::new(unique("missing")))),
        Err(RepositoryError::Conflict)
    );
}

#[test]
fn delivery_round_trip_keeps_revision_and_order_uniqueness() {
    let Some(store) = store() else { return };
    let mut delivery = new_delivery(&unique("delivery"));
    delivery.record_authorization_revision(AuthorizationRevision::new("zed-d"));
    DeliveryRepository::add(&store, &delivery).expect("add");

    let same_order = Delivery::new(
        DeliveryId::new(unique("delivery")),
        delivery.order_id().clone(),
        delivery.pickup_address().clone(),
        delivery.dropoff_address().clone(),
    );
    assert_eq!(
        DeliveryRepository::add(&store, &same_order),
        Err(RepositoryError::Conflict)
    );

    let by_order = DeliveryRepository::find_by_order(&store, delivery.order_id())
        .expect("find")
        .expect("delivery");
    assert_eq!(by_order.id(), delivery.id());
    assert_eq!(
        by_order.authorization_revision(),
        Some(&AuthorizationRevision::new("zed-d"))
    );

    let courier = unique("courier");
    DeliveryRepository::save(&store, &assigned(&by_order, &courier)).expect("save");
    let current = store
        .active_delivery_for(&CourierId::new(&courier))
        .expect("read model")
        .expect("current delivery");
    assert_eq!(&current.delivery_id, delivery.id());
    assert_eq!(current.state, State::Assigned);
}

#[test]
fn concurrent_updates_conflict_and_only_one_wins() {
    let Some(store) = store() else { return };
    let store = Arc::new(store);
    let delivery = new_delivery(&unique("delivery"));
    DeliveryRepository::add(&*store, &delivery).expect("add");

    let writers = 2;
    let barrier = Arc::new(Barrier::new(writers));
    let handles: Vec<_> = (0..writers)
        .map(|writer| {
            let store = Arc::clone(&store);
            let barrier = Arc::clone(&barrier);
            let id = delivery.id().clone();
            std::thread::spawn(move || {
                store.run(|| {
                    let loaded = DeliveryRepository::get(&*store, &id)?.expect("delivery");
                    // Both writers loaded version 0 before either saves.
                    barrier.wait();
                    let courier = format!("courier-{writer}");
                    let changed = assigned(&loaded, &courier);
                    DeliveryRepository::save(&*store, &changed)?;
                    DeliveryOutbox::add(
                        &*store,
                        OutboxMessage::delivery(DomainEvent::CourierAssignedToDelivery {
                            delivery_id: id.clone(),
                            order_id: loaded.order_id().clone(),
                            courier_id: CourierId::new(courier),
                            created_at_epoch_seconds: None,
                        }),
                    )?;
                    Ok::<_, TestError>(())
                })
            })
        })
        .collect();
    let results: Vec<_> = handles
        .into_iter()
        .map(|handle| handle.join().expect("writer"))
        .collect();

    assert_eq!(results.iter().filter(|result| result.is_ok()).count(), 1);
    assert_eq!(
        results
            .iter()
            .filter(|result| matches!(
                result,
                Err(TestError::Repository(
                    RepositoryError::ConcurrentModification
                ))
            ))
            .count(),
        1
    );
    let stored = DeliveryRepository::get(&*store, delivery.id())
        .expect("get")
        .expect("delivery");
    assert_eq!(stored.version(), 1);
    // The loser's Outbox row rolled back with its aggregate write.
    let rows = outbox_rows(delivery.id().as_str());
    assert_eq!(rows.len(), 1);
    let (id, event_type, version, payload, metadata) = &rows[0];
    assert_eq!(event_type, "nomnom.delivery.v1.CourierAssignedToDelivery");
    assert_eq!(*version, 1);
    assert!(payload.starts_with(b"courier-"));
    assert!(metadata.contains("00-test"));
    assert_eq!(
        id,
        &event_id("delivery", delivery.id().as_str(), 1, event_type).to_string()
    );
}

#[test]
fn unit_of_work_commits_or_rolls_back_aggregate_and_outbox_together() {
    let Some(store) = store() else { return };
    let delivery = new_delivery(&unique("delivery"));
    let event = DomainEvent::CourierAssignedToDelivery {
        delivery_id: delivery.id().clone(),
        order_id: delivery.order_id().clone(),
        courier_id: CourierId::new("courier-1"),
        created_at_epoch_seconds: None,
    };
    // No contract: the mapper returns None and nothing is written.
    let unpublished = DomainEvent::DeliveryCancelled {
        delivery_id: delivery.id().clone(),
        order_id: delivery.order_id().clone(),
        released_courier_id: None,
        reason: None,
    };

    let rolled_back: Result<(), TestError> = store.run(|| {
        DeliveryRepository::add(&store, &delivery)?;
        DeliveryOutbox::add(&store, OutboxMessage::delivery(event.clone()))?;
        Err(TestError::Abort)
    });
    assert_eq!(rolled_back, Err(TestError::Abort));
    assert_eq!(DeliveryRepository::get(&store, delivery.id()), Ok(None));
    assert!(outbox_rows(delivery.id().as_str()).is_empty());

    let committed: Result<(), TestError> = store.run(|| {
        DeliveryRepository::add(&store, &delivery)?;
        DeliveryOutbox::add(&store, OutboxMessage::delivery(event.clone()))?;
        DeliveryOutbox::add(&store, OutboxMessage::delivery(unpublished.clone()))?;
        Ok(())
    });
    assert_eq!(committed, Ok(()));
    assert!(
        DeliveryRepository::get(&store, delivery.id())
            .expect("get")
            .is_some()
    );
    let rows = outbox_rows(delivery.id().as_str());
    assert_eq!(rows.len(), 1);
    assert_eq!(rows[0].1, "nomnom.delivery.v1.CourierAssignedToDelivery");
    assert_eq!(rows[0].2, 0);
}

#[test]
fn offers_locations_and_reconciliation_reads() {
    let Some(store) = store() else { return };
    let courier = unique("courier");
    let courier_id = CourierId::new(&courier);
    CourierRepository::add(&store, &Courier::new(courier_id.clone())).expect("courier");
    let delivery = new_delivery(&unique("delivery"));
    DeliveryRepository::add(&store, &delivery).expect("delivery");

    let offer = DeliveryOffer::new(
        DeliveryOfferId::new(unique("offer")),
        delivery.id().clone(),
        courier_id.clone(),
        100,
        160,
    )
    .expect("offer");
    DeliveryOfferRepository::add(&store, &offer).expect("add offer");
    let pending = store.pending_offers_for(&courier_id).expect("pending");
    assert_eq!(pending.len(), 1);
    assert_eq!(pending[0].expires_at_epoch_seconds, 160);

    let rejected = DeliveryOffer::restore(
        offer.id().clone(),
        offer.delivery_id().clone(),
        courier_id.clone(),
        100,
        160,
        OfferState::Rejected,
    )
    .expect("rejected");
    DeliveryOfferRepository::save(&store, &rejected).expect("save offer");
    assert_eq!(
        DeliveryOfferRepository::save(&store, &rejected),
        Err(RepositoryError::ConcurrentModification)
    );
    let offers = store.list_for_delivery(delivery.id()).expect("offers");
    assert_eq!(offers.len(), 1);
    assert_eq!(offers[0].state(), OfferState::Rejected);
    assert!(
        store
            .pending_offers_for(&courier_id)
            .expect("pending")
            .is_empty()
    );

    let position = CourierPosition {
        courier_id: courier_id.clone(),
        location: Location::new(55.75, 37.61).expect("location"),
        recorded_at_epoch_seconds: 42,
    };
    store.record(&position).expect("record");
    store.record(&position).expect("upsert");
    assert_eq!(store.latest(&courier_id), Ok(Some(position)));

    let ids = vec![courier.clone(), unique("unknown")];
    assert_eq!(store.registered_couriers(&ids), Ok(vec![courier.clone()]));
    assert_eq!(
        store.record_courier_revision(&courier, AuthorizationRevision::new("zed-r")),
        Ok(true)
    );
    let stored = CourierRepository::get(&store, &courier_id)
        .expect("get")
        .expect("courier");
    assert_eq!(stored.version(), 1, "a revision write bumps the version");

    let loaded = DeliveryRepository::get(&store, delivery.id())
        .expect("get")
        .expect("delivery");
    DeliveryRepository::save(&store, &assigned(&loaded, &courier)).expect("assign");
    assert_eq!(
        store.assigned_couriers(&[delivery.id().as_str().to_owned()]),
        Ok(vec![(delivery.id().as_str().to_owned(), courier.clone())])
    );
    assert_eq!(
        store.record_assignment_revision(
            delivery.id().as_str(),
            "other-courier",
            AuthorizationRevision::new("zed-x")
        ),
        Ok(false)
    );
}

/// FR-563, AC-693: a delivered delivery keeps `courier_id` but is not an
/// active assignment for reconciliation.
#[test]
fn reconciliation_reads_only_active_assignments() {
    let Some(store) = store() else { return };
    let courier = unique("courier");
    let courier_id = CourierId::new(&courier);
    let delivery = new_delivery(&unique("delivery"));
    DeliveryRepository::add(&store, &delivery).expect("delivery");
    DeliveryRepository::save(&store, &assigned(&delivery, &courier)).expect("assign");
    let id = delivery.id().as_str().to_owned();
    assert_eq!(
        store.assigned_couriers(std::slice::from_ref(&id)),
        Ok(vec![(id.clone(), courier.clone())])
    );
    // FR-563: the page carries the row version the missing candidate is keyed by.
    let stored = DeliveryRepository::get(&store, delivery.id())
        .expect("get")
        .expect("delivery");
    let page = store.assignment_page("", 10_000).expect("page");
    assert!(page.contains(&(id.clone(), courier.clone(), stored.version())));
    CourierRepository::add(&store, &Courier::new(courier_id.clone())).expect("courier");
    let registered = CourierRepository::get(&store, &courier_id)
        .expect("get")
        .expect("courier");
    assert!(
        store
            .courier_page("", 100_000)
            .expect("couriers")
            .contains(&(courier.clone(), registered.version()))
    );

    let mut closed = DeliveryRepository::get(&store, delivery.id())
        .expect("get")
        .expect("delivery");
    closed.confirm_pickup(&courier_id).expect("pickup");
    closed.complete(&courier_id).expect("complete");
    DeliveryRepository::save(&store, &closed).expect("deliver");

    assert_eq!(
        store.assigned_couriers(std::slice::from_ref(&id)),
        Ok(vec![])
    );
    assert!(
        store
            .assignment_page("", 10_000)
            .expect("page")
            .iter()
            .all(|(delivery_id, _, _)| delivery_id != &id)
    );
    assert_eq!(
        store.record_assignment_revision(&id, &courier, AuthorizationRevision::new("zed-c")),
        Ok(false)
    );
}

/// FR-542: the locator reads latest positions of available couriers only.
#[test]
fn available_positions_are_positions_of_available_couriers() {
    let Some(store) = store() else { return };
    let available = CourierId::new(unique("available"));
    let busy = CourierId::new(unique("busy"));
    CourierRepository::add(
        &store,
        &Courier::restore(available.clone(), CourierStatus::Available),
    )
    .expect("available courier");
    CourierRepository::add(&store, &Courier::restore(busy.clone(), CourierStatus::Busy))
        .expect("busy courier");
    let position = |courier_id: &CourierId| CourierPosition {
        courier_id: courier_id.clone(),
        location: Location::new(52.37, 4.89).expect("location"),
        recorded_at_epoch_seconds: 7,
    };
    store.record(&position(&available)).expect("record");
    store.record(&position(&busy)).expect("record");

    let positions = store.available_positions().expect("positions");

    assert!(positions.contains(&position(&available)));
    assert!(positions.iter().all(|position| position.courier_id != busy));
}

#[tokio::test(flavor = "multi_thread")]
async fn works_inside_a_multi_thread_runtime() {
    let Some(store) = store() else { return };
    let id = CourierId::new(unique("courier"));
    CourierRepository::add(&store, &Courier::new(id.clone())).expect("add");
    assert!(CourierRepository::get(&store, &id).expect("get").is_some());
}

#[tokio::test(flavor = "current_thread")]
async fn works_inside_a_current_thread_runtime() {
    let Some(store) = store() else { return };
    let id = CourierId::new(unique("courier"));
    let result: Result<(), TestError> = store.run(|| {
        CourierRepository::add(&store, &Courier::new(id.clone()))?;
        Ok(())
    });
    assert_eq!(result, Ok(()));
    assert!(CourierRepository::get(&store, &id).expect("get").is_some());
}

#[test]
fn expired_pending_offers_are_listed_by_deadline_and_stored_as_expired() {
    let Some(store) = store() else { return };
    let courier_id = CourierId::new(unique("courier"));
    let delivery = new_delivery(&unique("delivery"));
    DeliveryRepository::add(&store, &delivery).expect("delivery");
    // Deadlines far in the past keep other tests' offers out of the batch.
    let base = -2_000_000_000;
    let offer = |deadline: i64| {
        let offer = DeliveryOffer::new(
            DeliveryOfferId::new(unique("offer")),
            delivery.id().clone(),
            courier_id.clone(),
            base,
            deadline,
        )
        .expect("offer");
        DeliveryOfferRepository::add(&store, &offer).expect("add offer");
        offer
    };
    let later = offer(base + 20);
    let earlier = offer(base + 10);
    let open = offer(base + 30);
    let ours = |offers: Vec<DeliveryOffer>| -> Vec<DeliveryOfferId> {
        offers
            .into_iter()
            .filter(|offer| offer.delivery_id() == delivery.id())
            .map(|offer| offer.id().clone())
            .collect()
    };

    // The deadline itself is not past: `open` (base + 30) is not listed.
    let listed = store.list_expired_pending(base + 30, 1000).expect("list");
    assert_eq!(ours(listed), vec![earlier.id().clone(), later.id().clone()]);
    assert_eq!(
        ours(store.list_expired_pending(base + 30, 1).expect("limit")).len(),
        1
    );

    let mut expired = earlier.clone();
    expired.expire(base + 30).expect("expire");
    DeliveryOfferRepository::save(&store, &expired).expect("save expired");
    assert_eq!(
        DeliveryOfferRepository::get(&store, earlier.id())
            .expect("get")
            .expect("offer")
            .state(),
        OfferState::Expired
    );
    assert_eq!(
        ours(store.list_expired_pending(base + 30, 1000).expect("list")),
        vec![later.id().clone()]
    );
    assert!(open.is_open_at(base + 30));
}

#[test]
fn failure_reason_code_and_detail_round_trip_and_legacy_text_reads_as_other() {
    let Some(store) = store() else { return };
    let courier = unique("courier");
    let mut delivery = new_delivery(&unique("delivery"));
    delivery.assign(CourierId::new(&courier)).expect("assign");
    DeliveryRepository::add(&store, &delivery).expect("delivery");
    let mut failed = DeliveryRepository::get(&store, delivery.id())
        .expect("get")
        .expect("delivery");
    let reason = DeliveryFailureReason::new("no_access", Some("gate locked")).expect("reason");
    failed
        .fail(reason.clone(), &FailureReporter::Dispatcher)
        .expect("fail");
    DeliveryRepository::save(&store, &failed).expect("save failed");
    let stored = DeliveryRepository::get(&store, delivery.id())
        .expect("get")
        .expect("delivery");
    assert_eq!(stored.failure_reason(), Some(&reason));

    // A row written before the catalog: free text, no detail.
    execute(
        "UPDATE deliveries SET failure_reason = 'customer  absent', failure_detail = NULL
         WHERE id = $1",
        delivery.id().as_str(),
    );
    let legacy = DeliveryRepository::get(&store, delivery.id())
        .expect("legacy row does not fail")
        .expect("delivery");
    let legacy_reason = legacy.failure_reason().expect("reason");
    assert_eq!(legacy_reason.code(), DeliveryFailureCode::Other);
    assert_eq!(legacy_reason.detail(), Some("customer absent"));
}

#[test]
fn waiting_deliveries_without_pending_offer_page_in_creation_order() {
    let Some(store) = store() else { return };
    // A creation time far in the past, unique per run, keeps the rows of
    // other tests of a shared database before or after ours.
    let nanos = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .expect("clock")
        .as_nanos();
    let base = -1_000_000_000 - i64::try_from(nanos % 1_000_000_000).expect("fits") * 4;
    let prefix = unique("waiting");
    let waiting = |suffix: &str, created_at: i64| {
        let delivery =
            new_delivery(&format!("{prefix}-{suffix}")).with_created_at(Some(created_at));
        DeliveryRepository::add(&store, &delivery).expect("delivery");
        delivery
    };
    let first = waiting("1", base + 1);
    let offered = waiting("2", base + 2);
    let third = waiting("3", base + 2);
    let assigned_one = assigned(&waiting("4", base + 3), &unique("courier"));
    DeliveryRepository::save(&store, &assigned_one).expect("assign");
    let mut pending = DeliveryOffer::new(
        DeliveryOfferId::new(unique("offer")),
        offered.id().clone(),
        CourierId::new(unique("courier")),
        base,
        base + 60,
    )
    .expect("offer");
    DeliveryOfferRepository::add(&store, &pending).expect("pending offer");
    let start = DeliveryPosition {
        created_at_epoch_seconds: base,
        delivery_id: DeliveryId::new(""),
    };
    let ours = |deliveries: Vec<Delivery>| -> Vec<DeliveryId> {
        deliveries
            .into_iter()
            .map(|delivery| delivery.id().clone())
            .filter(|id| id.as_str().starts_with(&prefix))
            .collect()
    };

    let page = store
        .list_waiting_without_pending_offer(Some(&start), 2)
        .expect("first page");
    assert_eq!(
        ours(page.clone()),
        vec![first.id().clone(), third.id().clone()]
    );
    let next = DeliveryPosition::of(page.last().expect("full page"));
    let rest = store
        .list_waiting_without_pending_offer(Some(&next), 1000)
        .expect("next page");
    assert!(ours(rest).is_empty(), "assigned and offered are not listed");

    pending
        .reject(&pending.courier_id().clone(), base)
        .expect("reject");
    DeliveryOfferRepository::save(&store, &pending).expect("rejected");
    assert_eq!(
        ours(
            store
                .list_waiting_without_pending_offer(Some(&start), 3)
                .expect("after rejection")
        ),
        vec![first.id().clone(), offered.id().clone(), third.id().clone()]
    );
    assert!(
        !store
            .list_waiting_without_pending_offer(None, 1)
            .expect("no cursor")
            .is_empty()
    );
}

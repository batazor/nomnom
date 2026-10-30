//! Integration-тесты PostgreSQL-хранилища (ADR 0001 контекста) против
//! настоящего PostgreSQL из testcontainers (`tests/support`). Каждый тест
//! использует уникальные идентификаторы, поэтому база общая.

mod support;

use delivery::slices::courier::domains::courier::MarketId;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::{Arc, Barrier};
use std::time::{SystemTime, UNIX_EPOCH};

use delivery::di;
use delivery::infrastructure::locator::{CourierPositions, LocatedCourier};
use delivery::infrastructure::reconcile::ReconciliationStore;
use delivery::infrastructure::repository::{
    MIGRATION_SETS, OutboxMapper, OutboxRecord, Store, event_id, migrate,
};
use delivery::pkg::config::database;
use delivery::pkg::persistence::{AuthorizationRevision, RepositoryError};
use delivery::pkg::unit_of_work::{UnitOfWork, UnitOfWorkError};
use delivery::slices::courier::applications::update_courier_location::{
    CourierLocationStore, CourierPosition,
};
use delivery::slices::courier::domains::courier::{
    Courier, CourierId, CourierName, CourierRepository, CourierStatus, Location,
};
use delivery::slices::delivery::applications::assignment_process::CandidateDeliveries;
use delivery::slices::delivery::applications::execution::get_courier_current_delivery::CourierCurrentDeliveryReadModel;
use delivery::slices::delivery::applications::offer::list_available_delivery_offers::PendingDeliveryOffersReadModel;
use delivery::slices::delivery::applications::offers_of_delivery::OffersOfDelivery;
use delivery::slices::delivery::applications::outbox::{DeliveryOutbox, OutboxMessage};
use delivery::slices::delivery::domains::delivery::{
    Delivery, DeliveryFailureCode, DeliveryFailureReason, DeliveryId, DeliveryOffer,
    DeliveryOfferId, DeliveryOfferRepository, DeliveryRepository, DomainEvent, ExpectedTime,
    FailureReporter, OfferState, OrderId, State,
};

fn config() -> database::Config {
    support::database()
}

/// Store over the migrated test database.
fn store() -> Store {
    Store::connect(&config())
        .expect("connect")
        .with_outbox_mapper(Arc::new(TestMapper))
}

/// Publishes only CourierAssignedToDelivery; the payload is the courier id.
struct TestMapper;

impl OutboxMapper for TestMapper {
    fn map(&self, message: &OutboxMessage) -> Option<OutboxRecord> {
        match &message.event {
            DomainEvent::CourierAssignedToDelivery { courier_id, .. } => Some(OutboxRecord {
                event_type: "nomnom.delivery.v1.CourierAssignedToDelivery".to_owned(),
                payload: courier_id.as_str().as_bytes().to_vec(),
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
        MarketId::new("berlin").expect("market id"),
    )
}

fn assigned(delivery: &Delivery, courier: &str) -> Delivery {
    Delivery::restore(
        delivery.id().clone(),
        delivery.order_id().clone(),
        MarketId::new("berlin").expect("market id"),
        Some(CourierId::new(courier)),
        State::Assigned,
        None,
    )
    .expect("assigned")
    .with_authorization_revision(delivery.authorization_revision().cloned())
    .with_version(delivery.version())
}

/// `(event_id, event_type, aggregate_version, payload, traceparent)` of the aggregate.
type OutboxRow = (String, String, i64, Vec<u8>, String);

fn outbox_rows(aggregate_id: &str) -> Vec<OutboxRow> {
    let config = config();
    let id = aggregate_id.to_owned();
    tokio::runtime::Builder::new_current_thread()
        .enable_all()
        .build()
        .expect("runtime")
        .block_on(async move {
            let mut connection = <sqlx::PgConnection as sqlx::Connection>::connect(&config.uri)
                .await
                .expect("connect");
            sqlx::query_as(
                "SELECT event_id::text, event_type, aggregate_version, payload, traceparent
                 FROM delivery_outbox WHERE aggregate_id = $1 ORDER BY created_at, event_id",
            )
            .bind(id)
            .fetch_all(&mut connection)
            .await
            .expect("outbox")
        })
}

/// Runs one statement with a single text parameter (raw writes of old rows).
fn execute(statement: &'static str, parameter: &str) {
    let config = config();
    let parameter = parameter.to_owned();
    tokio::runtime::Builder::new_current_thread()
        .enable_all()
        .build()
        .expect("runtime")
        .block_on(async move {
            let mut connection = <sqlx::PgConnection as sqlx::Connection>::connect(&config.uri)
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

/// Startup of the process (`di::store`) applies every embedded migration
/// of every set to an empty database and leaves a migrated one as it is.
#[test]
fn startup_migrates_an_empty_database_and_skips_a_migrated_one() {
    let nanos = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .expect("clock")
        .as_nanos();
    let config = support::empty_database(&format!("startup_{nanos}"));

    di::store(&config).expect("startup on an empty database");
    let first = installed(&config);
    assert_eq!(
        first
            .iter()
            .map(|(id, version, _)| (*id, *version))
            .collect::<Vec<_>>(),
        embedded()
    );

    di::store(&config).expect("startup on a migrated database");
    assert_eq!(installed(&config), first);
    assert_eq!(migrate(&config).expect("no pending"), Vec::new());
}

/// Storage ids of the migration sets and their history tables are part of
/// the schema contract: a code refactoring must not change them.
#[test]
fn migration_sets_keep_their_storage_ids() {
    assert_eq!(
        MIGRATION_SETS
            .iter()
            .map(|set| (set.id, set.migrator.table_name.as_ref()))
            .collect::<Vec<_>>(),
        [
            ("messaging", "_sqlx_migrations_messaging"),
            ("couriers", "_sqlx_migrations_couriers"),
            ("deliveries", "_sqlx_migrations_deliveries"),
            ("simulator", "_sqlx_migrations_simulator"),
        ]
    );
}

/// Replicas starting together serialize on the advisory lock: one applies
/// every migration, the others find nothing to change.
#[test]
fn replicas_starting_together_apply_migrations_once() {
    let nanos = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .expect("clock")
        .as_nanos();
    let config = support::empty_database(&format!("replicas_{nanos}"));
    let barrier = Arc::new(Barrier::new(4));
    let replicas: Vec<_> = (0..4)
        .map(|_| {
            let (config, barrier) = (config.clone(), Arc::clone(&barrier));
            std::thread::spawn(move || {
                barrier.wait();
                migrate(&config).expect("startup migration")
            })
        })
        .collect();
    let mut applied: Vec<Vec<(&str, i64)>> = replicas
        .into_iter()
        .map(|replica| replica.join().expect("replica"))
        .collect();
    applied.sort_by_key(Vec::len);
    assert_eq!(applied, vec![vec![], vec![], vec![], embedded()]);
}

/// ADR 41: couriers and deliveries stored before Markets existed belong to the
/// launch Market `berlin`. The Market migrations backfill them explicitly and
/// make `market_id` required, without a default for new rows.
#[test]
fn market_migrations_backfill_the_launch_market() {
    const MARKET_MIGRATIONS: [(&str, i64); 2] = [("couriers", 3), ("deliveries", 7)];
    let nanos = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .expect("clock")
        .as_nanos();
    let config = support::empty_database(&format!("market_backfill_{nanos}"));
    support::off_runtime(|| {
        tokio::runtime::Builder::new_current_thread()
            .enable_all()
            .build()
            .expect("runtime")
            .block_on(async {
                let pool = sqlx::PgPool::connect(&config.uri).await.expect("connect");
                let apply = |market: bool| {
                    MIGRATION_SETS.iter().flat_map(move |set| {
                        set.migrator.iter().filter(move |migration| {
                            MARKET_MIGRATIONS
                                .iter()
                                .any(|(id, version)| *id == set.id && *version <= migration.version)
                                == market
                        })
                    })
                };
                for migration in apply(false) {
                    sqlx::raw_sql(migration.sql.clone())
                        .execute(&pool)
                        .await
                        .expect("migration before markets");
                }
                sqlx::raw_sql(sqlx::AssertSqlSafe(
                    "INSERT INTO couriers (id, name, status, version)
                     VALUES ('old-courier', 'Timur', 'available', 0);
                     INSERT INTO deliveries (id, order_id, state, version)
                     VALUES ('old-delivery', 'old-order', 'waiting', 0);",
                ))
                .execute(&pool)
                .await
                .expect("rows before markets");
                for migration in apply(true) {
                    sqlx::raw_sql(migration.sql.clone())
                        .execute(&pool)
                        .await
                        .expect("market migration");
                }

                let markets: Vec<(String,)> = sqlx::query_as(
                    "SELECT market_id FROM couriers WHERE id = 'old-courier'
                     UNION ALL
                     SELECT market_id FROM deliveries WHERE id = 'old-delivery'",
                )
                .fetch_all(&pool)
                .await
                .expect("backfilled markets");
                assert_eq!(
                    markets,
                    vec![("berlin".to_owned(),), ("berlin".to_owned(),)]
                );
                for statement in [
                    "INSERT INTO couriers (id, name, status, version)
                     VALUES ('new-courier', 'Anna', 'available', 0)",
                    "INSERT INTO deliveries (id, order_id, state, version)
                     VALUES ('new-delivery', 'new-order', 'waiting', 0)",
                    "INSERT INTO couriers (id, name, status, version, market_id)
                     VALUES ('bad-courier', 'Anna', 'available', 0, 'Berlin')",
                ] {
                    assert!(
                        sqlx::raw_sql(sqlx::AssertSqlSafe(statement))
                            .execute(&pool)
                            .await
                            .is_err(),
                        "{statement}"
                    );
                }
                pool.close().await;
            });
    });
}

/// Embedded migrations as `(id, version)`, sets in their order.
fn embedded() -> Vec<(&'static str, i64)> {
    MIGRATION_SETS
        .iter()
        .flat_map(|set| {
            set.migrator
                .iter()
                .map(|migration| (set.id, migration.version))
        })
        .collect()
}

/// Applied migrations of every set, in set order, with the time each was
/// installed.
fn installed(config: &database::Config) -> Vec<(&'static str, i64, String)> {
    support::off_runtime(|| {
        tokio::runtime::Builder::new_current_thread()
            .enable_all()
            .build()
            .expect("runtime")
            .block_on(async {
                let pool = sqlx::PgPool::connect(&config.uri).await.expect("connect");
                let mut installed = Vec::new();
                for set in &MIGRATION_SETS {
                    let statement = format!(
                        "SELECT version, installed_on::text FROM {} \
                         WHERE success ORDER BY version",
                        set.migrator.table_name
                    );
                    let rows: Vec<(i64, String)> = sqlx::query_as(sqlx::AssertSqlSafe(statement))
                        .fetch_all(&pool)
                        .await
                        .expect("applied migrations");
                    installed.extend(rows.into_iter().map(|(version, at)| (set.id, version, at)));
                }
                pool.close().await;
                installed
            })
    })
}

#[test]
fn empty_dsn_fails_fast() {
    let Err(error) = Store::connect(&database::Config::default()) else {
        panic!("an empty DSN must fail");
    };
    assert!(error.contains("DATABASE__URI"));
}

#[test]
fn courier_round_trip_with_optimistic_versions() {
    let store = store();
    let id = unique("courier");
    let mut courier = Courier::new(
        CourierId::new(&id),
        CourierName::new("Timur").expect("courier name"),
        MarketId::new("berlin").expect("market id"),
    );
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

    let busy = Courier::restore(
        CourierId::new(&id),
        CourierName::new("Timur").expect("courier name"),
        MarketId::new("berlin").expect("market id"),
        CourierStatus::Busy,
    )
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
        CourierRepository::save(
            &store,
            &Courier::new(
                CourierId::new(unique("missing")),
                CourierName::new("Timur").expect("courier name"),
                MarketId::new("berlin").expect("market id")
            )
        ),
        Err(RepositoryError::Conflict)
    );
}

/// ADR 36: every courier write, inside or outside a Unit of Work, stores the
/// Courier entity key for the Router; a conflicting write stores nothing.
#[test]
fn courier_writes_notify_the_courier_entity() {
    let store = store();
    let id = unique("courier");
    let courier = Courier::new(
        CourierId::new(&id),
        CourierName::new("Timur").expect("courier name"),
        MarketId::new("berlin").expect("market id"),
    );
    CourierRepository::add(&store, &courier).expect("add");
    assert_eq!(
        CourierRepository::add(&store, &courier),
        Err(RepositoryError::Conflict)
    );
    let available = Courier::restore(
        CourierId::new(&id),
        CourierName::new("Timur").expect("courier name"),
        MarketId::new("berlin").expect("market id"),
        CourierStatus::Available,
    )
    .with_version(courier.version());
    CourierRepository::save(&store, &available).expect("save");
    assert_eq!(
        CourierRepository::save(&store, &available),
        Err(RepositoryError::ConcurrentModification)
    );

    let rows = outbox_rows(&id);
    assert_eq!(rows.len(), 2, "one notification per committed write");
    for (_, event_type, _, payload, traceparent) in rows {
        assert_eq!(event_type, "CourierChanged");
        assert_eq!(traceparent, "");
        assert_eq!(
            serde_json::from_slice::<serde_json::Value>(&payload).expect("json"),
            serde_json::json!({ "__typename": "Courier", "id": id })
        );
    }
}

/// FR-621, ADR 41: every committed courier write also notifies the Courier
/// Pool of the courier's Market, keyed by market_id. Only this test writes
/// the Market `pool-notifications`, but reruns against the same database
/// keep older rows, so the test counts at least its own two writes.
#[test]
fn courier_writes_notify_the_courier_pool() {
    const MARKET: &str = "pool-notifications";
    let store = store();
    let before = outbox_rows(MARKET).len();
    let id = unique("courier");
    let courier = Courier::new(
        CourierId::new(&id),
        CourierName::new("Timur").expect("courier name"),
        MarketId::new(MARKET).expect("market id"),
    );
    CourierRepository::add(&store, &courier).expect("add");
    let mut retired = Courier::restore(
        CourierId::new(&id),
        CourierName::new("Timur").expect("courier name"),
        MarketId::new(MARKET).expect("market id"),
        CourierStatus::Unavailable,
    )
    .with_version(courier.version());
    retired.retire(10).expect("retire");
    CourierRepository::save(&store, &retired).expect("save");

    let rows = outbox_rows(MARKET);
    assert!(rows.len() >= before + 2, "one pool notification per write");
    for (_, event_type, _, payload, _) in rows {
        assert_eq!(event_type, "CourierPoolChanged");
        assert_eq!(
            serde_json::from_slice::<serde_json::Value>(&payload).expect("json"),
            serde_json::json!({ "__typename": "CourierPool", "id": MARKET })
        );
    }
    let stored = CourierRepository::get(&store, &CourierId::new(&id))
        .expect("get")
        .expect("courier");
    assert_eq!(stored.name().as_str(), "Timur");
    assert_eq!(stored.retired_at_epoch_seconds(), Some(10));
}

/// ADR 37: Debezium builds the Kafka message from the record contract
/// columns only; the publication streams inserts and nothing else.
#[tokio::test(flavor = "multi_thread")]
async fn outbox_table_is_the_cdc_record_contract() {
    let db = sqlx::PgPool::connect(&config().uri).await.expect("pool");
    let columns: Vec<(String, String, String)> = sqlx::query_as(
        "SELECT column_name::text, data_type::text, is_nullable::text
         FROM information_schema.columns
         WHERE table_name = 'delivery_outbox' ORDER BY column_name",
    )
    .fetch_all(&db)
    .await
    .expect("columns");
    let column =
        |name: &str, data_type: &str| (name.to_owned(), data_type.to_owned(), "NO".to_owned());
    assert_eq!(
        columns,
        vec![
            column("aggregate_id", "text"),
            column("aggregate_type", "text"),
            column("aggregate_version", "bigint"),
            column("created_at", "timestamp with time zone"),
            column("event_id", "uuid"),
            column("event_type", "text"),
            column("occurred_at", "timestamp with time zone"),
            column("payload", "bytea"),
            column("topic", "text"),
            column("traceparent", "text"),
        ]
    );

    let publication: (bool, bool, bool, bool) = sqlx::query_as(
        "SELECT pubinsert, pubupdate, pubdelete, pubtruncate
         FROM pg_publication WHERE pubname = 'delivery_outbox'",
    )
    .fetch_one(&db)
    .await
    .expect("publication");
    assert_eq!(publication, (true, false, false, false));
    let tables: Vec<String> = sqlx::query_scalar(
        "SELECT tablename::text FROM pg_publication_tables WHERE pubname = 'delivery_outbox'",
    )
    .fetch_all(&db)
    .await
    .expect("publication tables");
    assert_eq!(tables, vec!["delivery_outbox".to_owned()]);
}

/// ADR 37: every Outbox write — the courier CTE, a courier notification of a
/// delivery write and an Integration Event — deletes rows older than an hour
/// and keeps fresh ones. Writes of parallel tests delete expired rows too, so
/// only the absence after each write is asserted.
#[test]
fn outbox_writes_delete_rows_older_than_an_hour() {
    let store = store();
    let expired = unique("expired");
    let plant_expired = || {
        execute(
            "INSERT INTO delivery_outbox (event_id, event_type, aggregate_type, aggregate_id,
                 aggregate_version, topic, payload, created_at)
             VALUES (gen_random_uuid(), 'CourierChanged', 'Courier', $1, 0, 'test', '',
                 now() - interval '2 hours')",
            &expired,
        );
    };

    plant_expired();
    let courier = CourierId::new(unique("courier"));
    CourierRepository::add(
        &store,
        &Courier::new(
            courier.clone(),
            CourierName::new("Timur").expect("courier name"),
            MarketId::new("berlin").expect("market id"),
        ),
    )
    .expect("courier");
    assert!(outbox_rows(&expired).is_empty(), "courier CTE write");

    plant_expired();
    let delivery = new_delivery(&unique("delivery"));
    DeliveryRepository::add(&store, &assigned(&delivery, courier.as_str())).expect("delivery");
    assert!(outbox_rows(&expired).is_empty(), "courier notification");
    assert_eq!(outbox_rows(courier.as_str()).len(), 2);

    plant_expired();
    let event = DomainEvent::CourierAssignedToDelivery {
        delivery_id: delivery.id().clone(),
        order_id: delivery.order_id().clone(),
        courier_id: courier.clone(),
        created_at_epoch_seconds: None,
        expected_pickup_at_epoch_seconds: None,
        market_id: MarketId::new("berlin").expect("market id"),
    };
    DeliveryOutbox::add(&store, OutboxMessage::delivery(event)).expect("outbox");
    assert!(outbox_rows(&expired).is_empty(), "Integration Event");
    assert_eq!(outbox_rows(delivery.id().as_str()).len(), 1);
}

/// ADR 41: the Market of a courier and of a Delivery is stored and read back.
#[test]
fn courier_and_delivery_keep_their_market() {
    let store = store();
    let hamburg = MarketId::new("hamburg").expect("market id");
    let courier = Courier::new(
        CourierId::new(unique("courier")),
        CourierName::new("Timur").expect("courier name"),
        hamburg.clone(),
    );
    CourierRepository::add(&store, &courier).expect("add courier");
    let delivery = Delivery::new(
        DeliveryId::new(unique("delivery")),
        OrderId::new(unique("order")),
        hamburg.clone(),
    );
    DeliveryRepository::add(&store, &delivery).expect("add delivery");

    assert_eq!(
        CourierRepository::get(&store, courier.id())
            .expect("get courier")
            .map(|courier| courier.market_id().clone()),
        Some(hamburg.clone())
    );
    assert_eq!(
        DeliveryRepository::find_by_order(&store, delivery.order_id())
            .expect("find delivery")
            .map(|delivery| delivery.market_id().clone()),
        Some(hamburg)
    );
}

#[test]
fn delivery_round_trip_keeps_revision_and_order_uniqueness() {
    let store = store();
    let mut delivery = new_delivery(&unique("delivery"));
    delivery.record_authorization_revision(AuthorizationRevision::new("zed-d"));
    DeliveryRepository::add(&store, &delivery).expect("add");

    let same_order = Delivery::new(
        DeliveryId::new(unique("delivery")),
        delivery.order_id().clone(),
        MarketId::new("berlin").expect("market id"),
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
    let store = store();
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
                            expected_pickup_at_epoch_seconds: None,
                            market_id: MarketId::new("berlin").expect("market id"),
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
    let (id, event_type, version, payload, traceparent) = &rows[0];
    assert_eq!(event_type, "nomnom.delivery.v1.CourierAssignedToDelivery");
    assert_eq!(*version, 1);
    assert!(payload.starts_with(b"courier-"));
    // No current span: the header is empty, the column is never NULL (ADR 37).
    assert_eq!(traceparent, "");
    assert_eq!(
        id,
        &event_id("delivery", delivery.id().as_str(), 1, event_type).to_string()
    );
}

#[test]
fn unit_of_work_commits_or_rolls_back_aggregate_and_outbox_together() {
    let store = store();
    let delivery = new_delivery(&unique("delivery"));
    let event = DomainEvent::CourierAssignedToDelivery {
        delivery_id: delivery.id().clone(),
        order_id: delivery.order_id().clone(),
        courier_id: CourierId::new("courier-1"),
        created_at_epoch_seconds: None,
        expected_pickup_at_epoch_seconds: None,
        market_id: MarketId::new("berlin").expect("market id"),
    };
    // No contract: the mapper returns None and nothing is written.
    let unpublished = DomainEvent::DeliveryCancelled {
        delivery_id: delivery.id().clone(),
        order_id: delivery.order_id().clone(),
        released_courier_id: None,
        reason: None,
        market_id: MarketId::new("berlin").expect("market id"),
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
    let store = store();
    let courier = unique("courier");
    let courier_id = CourierId::new(&courier);
    CourierRepository::add(
        &store,
        &Courier::new(
            courier_id.clone(),
            CourierName::new("Timur").expect("courier name"),
            MarketId::new("berlin").expect("market id"),
        ),
    )
    .expect("courier");
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
    let ids = store.offer_ids_of(delivery.id()).expect("offers");
    assert_eq!(ids, vec![offer.id().clone()]);
    assert_eq!(
        DeliveryOfferRepository::get(&store, &ids[0])
            .expect("get")
            .expect("offer")
            .state(),
        OfferState::Rejected
    );
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
    store.record(&position, 0).expect("record");
    store.record(&position, 0).expect("upsert");
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
    let store = store();
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
    CourierRepository::add(
        &store,
        &Courier::new(
            courier_id.clone(),
            CourierName::new("Timur").expect("courier name"),
            MarketId::new("berlin").expect("market id"),
        ),
    )
    .expect("courier");
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
    closed.confirm_pickup(&courier_id, None).expect("pickup");
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

/// FR-542, ADR 41: the locator reads latest positions of the available
/// couriers of one Market only, each with its stored Market.
#[test]
fn available_positions_are_positions_of_available_couriers_of_the_market() {
    let store = store();
    let berlin = MarketId::new("berlin").expect("market id");
    let hamburg = MarketId::new("hamburg").expect("market id");
    let available = CourierId::new(unique("available"));
    let busy = CourierId::new(unique("busy"));
    let elsewhere = CourierId::new(unique("hamburg"));
    for (courier_id, market_id, status) in [
        (&available, &berlin, CourierStatus::Available),
        (&busy, &berlin, CourierStatus::Busy),
        (&elsewhere, &hamburg, CourierStatus::Available),
    ] {
        CourierRepository::add(
            &store,
            &Courier::restore(
                courier_id.clone(),
                CourierName::new("Timur").expect("courier name"),
                market_id.clone(),
                status,
            ),
        )
        .expect("seed courier");
    }
    let position = |courier_id: &CourierId| CourierPosition {
        courier_id: courier_id.clone(),
        location: Location::new(52.37, 4.89).expect("location"),
        recorded_at_epoch_seconds: 7,
    };
    let located = |courier_id: &CourierId, market_id: &MarketId| LocatedCourier {
        position: position(courier_id),
        market_id: market_id.clone(),
    };
    for courier_id in [&available, &busy, &elsewhere] {
        store.record(&position(courier_id), 0).expect("record");
    }

    let positions = store.available_positions(&berlin).expect("positions");

    assert!(positions.contains(&located(&available, &berlin)));
    assert!(positions.iter().all(|located| {
        located.position.courier_id != busy
            && located.position.courier_id != elsewhere
            && located.market_id == berlin
    }));
    assert!(
        store
            .available_positions(&hamburg)
            .expect("positions")
            .contains(&located(&elsewhere, &hamburg))
    );
    assert_eq!(
        store.located(&elsewhere),
        Ok(Some(located(&elsewhere, &hamburg)))
    );
    assert_eq!(store.located(&CourierId::new(unique("unknown"))), Ok(None));
}

#[tokio::test(flavor = "multi_thread")]
async fn works_inside_a_multi_thread_runtime() {
    let store = store();
    let id = CourierId::new(unique("courier"));
    CourierRepository::add(
        &store,
        &Courier::new(
            id.clone(),
            CourierName::new("Timur").expect("courier name"),
            MarketId::new("berlin").expect("market id"),
        ),
    )
    .expect("add");
    assert!(CourierRepository::get(&store, &id).expect("get").is_some());
}

#[tokio::test(flavor = "current_thread")]
async fn works_inside_a_current_thread_runtime() {
    let store = store();
    let id = CourierId::new(unique("courier"));
    let result: Result<(), TestError> = store.run(|| {
        CourierRepository::add(
            &store,
            &Courier::new(
                id.clone(),
                CourierName::new("Timur").expect("courier name"),
                MarketId::new("berlin").expect("market id"),
            ),
        )?;
        Ok(())
    });
    assert_eq!(result, Ok(()));
    assert!(CourierRepository::get(&store, &id).expect("get").is_some());
}

#[test]
fn failure_reason_code_and_detail_round_trip_and_legacy_text_reads_as_other() {
    let store = store();
    let courier = unique("courier");
    let mut delivery = new_delivery(&unique("delivery"));
    delivery
        .assign(CourierId::new(&courier), None)
        .expect("assign");
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

/// FR-610: expected times named by the courier round-trip; not named ones
/// stay empty, a reassignment clears the Pickup time.
#[test]
fn expected_times_round_trip_and_reassignment_clears_pickup_time() {
    let store = store();
    let courier = CourierId::new(unique("courier"));
    let delivery = new_delivery(&unique("delivery"));
    DeliveryRepository::add(&store, &delivery).expect("delivery");
    let read = || {
        DeliveryRepository::get(&store, delivery.id())
            .expect("get")
            .expect("delivery")
    };
    assert_eq!(read().expected_pickup_at(), None);
    assert_eq!(read().expected_delivered_at(), None);

    let mut assigned = read();
    assigned
        .assign(courier.clone(), Some(ExpectedTime::restore(1_600)))
        .expect("assign");
    DeliveryRepository::save(&store, &assigned).expect("save assigned");
    assert_eq!(
        read().expected_pickup_at(),
        Some(ExpectedTime::restore(1_600))
    );

    let mut released = read();
    released.unassign().expect("unassign");
    DeliveryRepository::save(&store, &released).expect("save released");
    assert_eq!(read().expected_pickup_at(), None);

    let mut picked = read();
    picked
        .assign(courier.clone(), Some(ExpectedTime::restore(1_700)))
        .expect("assign");
    picked
        .confirm_pickup(&courier, Some(ExpectedTime::restore(2_400)))
        .expect("pickup");
    DeliveryRepository::save(&store, &picked).expect("save picked up");
    let stored = read();
    assert_eq!(
        stored.expected_pickup_at(),
        Some(ExpectedTime::restore(1_700))
    );
    assert_eq!(
        stored.expected_delivered_at(),
        Some(ExpectedTime::restore(2_400))
    );

    // Inserted with the times too.
    let mut inserted = new_delivery(&unique("delivery"));
    inserted
        .assign(courier.clone(), Some(ExpectedTime::restore(3_000)))
        .expect("assign");
    DeliveryRepository::add(&store, &inserted).expect("insert");
    assert_eq!(
        DeliveryRepository::get(&store, inserted.id())
            .expect("get")
            .expect("delivery")
            .expected_pickup_at(),
        Some(ExpectedTime::restore(3_000))
    );
}

#[test]
fn courier_candidate_wakes_the_oldest_waiting_delivery_not_offered_to_it() {
    let store = store();
    // A Market of its own keeps the deliveries of other tests out.
    let nanos = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .expect("clock")
        .as_nanos();
    let market = MarketId::new(&format!("m{}", nanos % 1_000_000_000_000)).expect("market id");
    let other_market =
        MarketId::new(&format!("o{}", nanos % 1_000_000_000_000)).expect("market id");
    let courier_id = CourierId::new(unique("courier"));
    CourierRepository::add(
        &store,
        &Courier::new(
            courier_id.clone(),
            CourierName::new("Timur").expect("courier name"),
            market.clone(),
        ),
    )
    .expect("courier");
    let waiting = |market: &MarketId, created_at: i64| {
        let delivery = Delivery::new(
            DeliveryId::new(unique("delivery")),
            OrderId::new(unique("order")),
            market.clone(),
        )
        .with_created_at(Some(created_at));
        DeliveryRepository::add(&store, &delivery).expect("delivery");
        delivery
    };
    let offer = |delivery: &Delivery, courier: &CourierId| {
        let offer = DeliveryOffer::new(
            DeliveryOfferId::new(unique("offer")),
            delivery.id().clone(),
            courier.clone(),
            1_000,
            1_060,
        )
        .expect("offer");
        DeliveryOfferRepository::add(&store, &offer).expect("offer");
        offer
    };
    waiting(&other_market, 1);
    let mut rejected = offer(&waiting(&market, 2), &courier_id);
    rejected.reject(&courier_id, 1_000).expect("reject");
    DeliveryOfferRepository::save(&store, &rejected).expect("rejected");
    let pending_delivery = waiting(&market, 3);
    let mut pending = offer(&pending_delivery, &CourierId::new(unique("courier")));
    let oldest_free = waiting(&market, 4);
    waiting(&market, 5);

    // Offered to the courier before, an open offer and another Market are
    // skipped.
    assert_eq!(
        store.oldest_waiting_for(&courier_id),
        Ok(Some(oldest_free.id().clone()))
    );

    pending
        .reject(&pending.courier_id().clone(), 1_000)
        .expect("reject");
    DeliveryOfferRepository::save(&store, &pending).expect("rejected");
    assert_eq!(
        store.oldest_waiting_for(&courier_id),
        Ok(Some(pending_delivery.id().clone()))
    );
    assert_eq!(
        store.oldest_waiting_for(&CourierId::new(unique("courier"))),
        Ok(None),
        "an unknown courier has no Market"
    );
}

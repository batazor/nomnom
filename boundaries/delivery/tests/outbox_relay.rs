//! Integration-тест Outbox → `delivery-relay` → Kafka (ADR 13, ADR 31) против
//! настоящих PostgreSQL и Kafka. Запускается только при заданных
//! `DATABASE_URL` и `KAFKA_BROKERS`:
//!
//! `DATABASE_URL=postgres://postgres:postgres@localhost:5432/postgres KAFKA_BROKERS=localhost:9092 cargo test --test outbox_relay`
//!
//! Без переменных тесты завершаются сразу. Идентификаторы уникальны, поэтому
//! база и topic могут быть общими.

use std::collections::HashMap;
use std::sync::Arc;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::time::{Duration, Instant, SystemTime, UNIX_EPOCH};

use prost::Message as _;
use rdkafka::ClientConfig;
use rdkafka::consumer::{Consumer as _, StreamConsumer};
use rdkafka::message::{Headers as _, Message as _};
use tokio::sync::watch;

use delivery::di;
use delivery::infrastructure::grpc::v1;
use delivery::infrastructure::outbox::{
    KafkaPublisher, PgOutbox, RelayMetrics, RelayWorker, WATERMILL_UUID_HEADER,
};
use delivery::infrastructure::postgres::{OUTBOX_TOPIC, event_id, migrate};
use delivery::pkg::config::{DatabaseConfig, KafkaConfig, OutboxConfig};
use delivery::pkg::lifecycle::Worker as _;
use delivery::slices::courier::domains::courier::CourierId;
use delivery::slices::delivery::applications::outbox::{DeliveryOutbox, OutboxMessage};
use delivery::slices::delivery::domains::delivery::{
    Address, Delivery, DeliveryId, DeliveryRepository, DomainEvent, OrderId, State,
};

const TRACEPARENT: &str = "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01";

struct Env {
    database: DatabaseConfig,
    kafka: KafkaConfig,
}

fn env() -> Option<Env> {
    let url = std::env::var("DATABASE_URL")
        .ok()
        .filter(|v| !v.is_empty())?;
    let brokers = std::env::var("KAFKA_BROKERS")
        .ok()
        .filter(|v| !v.is_empty())?;
    Some(Env {
        database: DatabaseConfig { url, max_conns: 4 },
        kafka: KafkaConfig {
            brokers,
            client_id: "delivery-relay-test".to_owned(),
        },
    })
}

/// Tests share the Outbox table and the relay lock: run them one at a time.
static SERIAL: tokio::sync::Mutex<()> = tokio::sync::Mutex::const_new(());

fn unique(prefix: &str) -> String {
    static NEXT: AtomicUsize = AtomicUsize::new(0);
    let nanos = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .expect("clock")
        .as_nanos();
    format!("{prefix}-{nanos}-{}", NEXT.fetch_add(1, Ordering::Relaxed))
}

async fn pool(config: &DatabaseConfig) -> sqlx::PgPool {
    sqlx::PgPool::connect(&config.url).await.expect("pool")
}

#[tokio::test(flavor = "multi_thread")]
async fn relay_publishes_outbox_rows_with_envelope_headers() {
    let Some(env) = env() else { return };
    let _serial = SERIAL.lock().await;
    migrate(&env.database).expect("migrate");
    let db = pool(&env.database).await;

    // Main process write path: aggregate + mapped Integration Event.
    let store = di::postgres(&env.database).expect("store");
    let delivery_id = unique("delivery");
    let order_id = unique("order");
    let courier_id = unique("courier");
    let delivery = Delivery::new(
        DeliveryId::new(&delivery_id),
        OrderId::new(&order_id),
        Address::new("Restaurant street 1").expect("pickup"),
        Address::new("Customer street 2").expect("dropoff"),
    );
    DeliveryRepository::add(&store, &delivery).expect("add delivery");
    let assigned = Delivery::restore(
        delivery.id().clone(),
        delivery.order_id().clone(),
        Some(CourierId::new(&courier_id)),
        delivery.pickup_address().clone(),
        delivery.dropoff_address().clone(),
        State::Assigned,
        None,
    )
    .expect("assigned")
    .with_version(delivery.version());
    DeliveryRepository::save(&store, &assigned).expect("save delivery");
    let events = [
        DomainEvent::CourierAssignedToDelivery {
            delivery_id: DeliveryId::new(&delivery_id),
            order_id: OrderId::new(&order_id),
            courier_id: CourierId::new(&courier_id),
        },
        // Same transaction, same aggregate version: `event_type` keeps the
        // ids apart (ADR 31).
        DomainEvent::CourierUnassignedFromDelivery {
            delivery_id: DeliveryId::new(&delivery_id),
            order_id: OrderId::new(&order_id),
            courier_id: CourierId::new(&courier_id),
        },
        DomainEvent::DeliveryPickedUp {
            delivery_id: DeliveryId::new(&delivery_id),
            order_id: OrderId::new(&order_id),
            courier_id: CourierId::new(&courier_id),
        },
    ];
    for event in events {
        DeliveryOutbox::add(&store, OutboxMessage::delivery(event)).expect("outbox");
    }
    drop(store);

    let rows: Vec<(String, String, i64)> = sqlx::query_as(
        "SELECT event_id::text, event_type, aggregate_version FROM delivery_outbox
         WHERE aggregate_id = $1 ORDER BY seq",
    )
    .bind(&delivery_id)
    .fetch_all(&db)
    .await
    .expect("rows");
    assert_eq!(
        rows.iter().map(|row| row.1.as_str()).collect::<Vec<_>>(),
        vec![
            "nomnom.delivery.v1.CourierAssignedToDelivery",
            "nomnom.delivery.v1.CourierUnassignedFromDelivery",
            "nomnom.delivery.v1.DeliveryPickedUp"
        ]
    );
    let ids: std::collections::HashSet<&str> = rows.iter().map(|row| row.0.as_str()).collect();
    assert_eq!(ids.len(), rows.len(), "event_id is unique per event_type");
    for (id, event_type, version) in &rows {
        assert_eq!(
            id,
            &event_id("delivery", &delivery_id, *version, event_type).to_string()
        );
    }
    // A trace context written by an instrumented publisher reaches the header.
    sqlx::query(
        "UPDATE delivery_outbox SET metadata = jsonb_build_object('traceparent', $2::text)
         WHERE aggregate_id = $1",
    )
    .bind(&delivery_id)
    .bind(TRACEPARENT)
    .execute(&db)
    .await
    .expect("traceparent");

    let consumer: StreamConsumer = ClientConfig::new()
        .set("bootstrap.servers", &env.kafka.brokers)
        .set("group.id", unique("delivery-relay-test"))
        .set("auto.offset.reset", "earliest")
        .set("enable.auto.commit", "false")
        .create()
        .expect("consumer");

    // Relay worker under the advisory lock.
    let outbox = Arc::new(PgOutbox::connect(&env.database).await.expect("outbox"));
    let publisher = KafkaPublisher::new(&env.kafka, Duration::from_secs(30)).expect("publisher");
    let config = OutboxConfig {
        poll_interval: Duration::from_millis(100),
        ..OutboxConfig::default()
    };
    let (stop_tx, stop_rx) = watch::channel(false);
    let relay = tokio::spawn(
        RelayWorker::new(outbox, publisher, config, RelayMetrics::global()).run(stop_rx),
    );

    let deadline = Instant::now() + Duration::from_secs(60);
    loop {
        let pending: i64 = sqlx::query_scalar(
            "SELECT count(*) FROM delivery_outbox WHERE aggregate_id = $1 AND published_at IS NULL",
        )
        .bind(&delivery_id)
        .fetch_one(&db)
        .await
        .expect("pending");
        if pending == 0 {
            break;
        }
        assert!(Instant::now() < deadline, "relay did not publish in time");
        tokio::time::sleep(Duration::from_millis(100)).await;
    }
    stop_tx.send(true).expect("stop");
    relay.await.expect("join").expect("relay");

    consumer.subscribe(&[OUTBOX_TOPIC]).expect("subscribe");
    let mut received = Vec::new();
    let deadline = Instant::now() + Duration::from_secs(60);
    while received.len() < rows.len() {
        let remaining = deadline.saturating_duration_since(Instant::now());
        let message = tokio::time::timeout(remaining, consumer.recv())
            .await
            .expect("messages in time")
            .expect("message");
        if message.key() != Some(delivery_id.as_bytes()) {
            continue;
        }
        let headers: HashMap<String, String> = message
            .headers()
            .expect("headers")
            .iter()
            .map(|header| {
                (
                    header.key.to_owned(),
                    String::from_utf8(header.value.expect("value").to_vec()).expect("utf8"),
                )
            })
            .collect();
        received.push((headers, message.payload().expect("payload").to_vec()));
    }

    for ((headers, payload), (id, event_type, version)) in received.iter().zip(&rows) {
        assert_eq!(headers["event_id"], *id);
        assert_eq!(headers[WATERMILL_UUID_HEADER], *id);
        assert_eq!(headers["event_type"], *event_type);
        assert_eq!(headers["aggregate_type"], "delivery");
        assert_eq!(headers["aggregate_id"], delivery_id);
        assert_eq!(headers["aggregate_version"], version.to_string());
        assert_eq!(headers["traceparent"], TRACEPARENT);
        let occurred_at = &headers["occurred_at"];
        assert!(occurred_at.ends_with('Z'), "{occurred_at}");
        assert!(!occurred_at.contains('+'), "{occurred_at}");
        assert_eq!(&occurred_at[10..11], "T");
        let _ = payload;
    }
    assert_eq!(
        v1::CourierAssignedToDelivery::decode(received[0].1.as_slice()).expect("decode"),
        v1::CourierAssignedToDelivery {
            order_id: order_id.clone(),
            delivery_id: delivery_id.clone(),
            courier_id: courier_id.clone(),
        }
    );
    assert_eq!(
        v1::CourierUnassignedFromDelivery::decode(received[1].1.as_slice()).expect("decode"),
        v1::CourierUnassignedFromDelivery {
            order_id: order_id.clone(),
            delivery_id: delivery_id.clone(),
            courier_id,
        }
    );
    assert_eq!(
        v1::DeliveryPickedUp::decode(received[2].1.as_slice()).expect("decode"),
        v1::DeliveryPickedUp {
            order_id,
            delivery_id,
        }
    );
}

#[tokio::test(flavor = "multi_thread")]
async fn only_one_relay_holds_the_lock() {
    let Some(env) = env() else { return };
    let _serial = SERIAL.lock().await;
    let first = PgOutbox::connect(&env.database).await.expect("first");
    let second = PgOutbox::connect(&env.database).await.expect("second");

    // Another test may hold the lock briefly; wait for it.
    let deadline = Instant::now() + Duration::from_secs(30);
    while !first.try_acquire_lock().await.expect("lock") {
        assert!(Instant::now() < deadline, "lock not acquired");
        tokio::time::sleep(Duration::from_millis(100)).await;
    }
    assert!(first.try_acquire_lock().await.expect("held"));
    assert!(!second.try_acquire_lock().await.expect("standby"));

    first.release_lock().await;
    assert!(second.try_acquire_lock().await.expect("failover"));
    second.release_lock().await;
}

#[tokio::test(flavor = "multi_thread")]
async fn retention_deletes_only_old_published_rows() {
    let Some(env) = env() else { return };
    let _serial = SERIAL.lock().await;
    migrate(&env.database).expect("migrate");
    let db = pool(&env.database).await;
    let aggregate_id = unique("retention");
    // (aggregate_version, published days ago).
    for (version, published_days_ago) in [(1_i64, Some(8_i32)), (2, Some(1)), (3, None)] {
        let id = event_id("delivery", &aggregate_id, version, "test.Event").to_string();
        sqlx::query(
            "INSERT INTO delivery_outbox (event_id, event_type, aggregate_type, aggregate_id,
                 aggregate_version, occurred_at, topic, payload, published_at)
             VALUES ($1::uuid, 'test.Event', 'delivery', $2, $3, now() - interval '9 days',
                 'test.retention', ''::bytea, now() - make_interval(days => $4))",
        )
        .bind(&id)
        .bind(&aggregate_id)
        .bind(version)
        .bind(published_days_ago)
        .execute(&db)
        .await
        .expect("insert");
    }

    let outbox = PgOutbox::connect(&env.database).await.expect("outbox");
    let deleted = outbox
        .cleanup(OutboxConfig::default().retention)
        .await
        .expect("cleanup");
    assert!(deleted >= 1);
    let left: Vec<i64> = sqlx::query_scalar(
        "SELECT aggregate_version FROM delivery_outbox WHERE aggregate_id = $1 ORDER BY seq",
    )
    .bind(&aggregate_id)
    .fetch_all(&db)
    .await
    .expect("left");
    assert_eq!(left, vec![2, 3]);

    // The unpublished row is in the backlog of its stream; clean up after.
    let backlog = outbox.backlog().await.expect("backlog");
    let (_, pending, age) = backlog
        .iter()
        .find(|(stream, _, _)| stream == "test.retention")
        .expect("stream");
    assert!(*pending >= 1);
    assert!(*age > 8.0 * 24.0 * 3600.0);
    sqlx::query("DELETE FROM delivery_outbox WHERE aggregate_id = $1")
        .bind(&aggregate_id)
        .execute(&db)
        .await
        .expect("cleanup rows");
}

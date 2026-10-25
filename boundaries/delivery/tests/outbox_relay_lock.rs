//! Integration test of the `delivery-relay` lock session (FR-537, AC-615)
//! against a real PostgreSQL. Runs only with `DATABASE_URL`:
//!
//! `DATABASE_URL=postgres://postgres:postgres@localhost:5432/postgres cargo test --test outbox_relay_lock`

use std::time::{Duration, Instant};

use delivery::infrastructure::outbox::PgOutbox;
use delivery::pkg::config::DatabaseConfig;

fn database() -> Option<DatabaseConfig> {
    let url = std::env::var("DATABASE_URL")
        .ok()
        .filter(|v| !v.is_empty())?;
    Some(DatabaseConfig { url, max_conns: 4 })
}

/// A terminated lock session is noticed before the next record and the
/// standby takes the lock over.
#[tokio::test(flavor = "multi_thread")]
async fn lost_lock_session_is_detected_before_the_next_record() {
    let Some(database) = database() else { return };
    let active = PgOutbox::connect(&database).await.expect("active");
    let standby = PgOutbox::connect(&database).await.expect("standby");
    assert!(!active.lock_alive().await, "no lock before acquiring");

    let deadline = Instant::now() + Duration::from_secs(30);
    while !active.try_acquire_lock().await.expect("lock") {
        assert!(Instant::now() < deadline, "lock not acquired");
        tokio::time::sleep(Duration::from_millis(100)).await;
    }
    assert!(active.lock_alive().await);

    let admin = sqlx::PgPool::connect(&database.url).await.expect("admin");
    let terminated: i64 = sqlx::query_scalar(
        "SELECT count(pg_terminate_backend(pid)) FROM pg_locks
         WHERE locktype = 'advisory' AND granted AND pid <> pg_backend_pid()",
    )
    .fetch_one(&admin)
    .await
    .expect("terminate lock session");
    assert!(terminated >= 1);

    assert!(!active.lock_alive().await, "a lost session is detected");
    assert!(
        !active.lock_alive().await,
        "and stays lost until re-acquired"
    );
    assert!(standby.try_acquire_lock().await.expect("failover"));
    standby.release_lock().await;
    active.release_lock().await;
}

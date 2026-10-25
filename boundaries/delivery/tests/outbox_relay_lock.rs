//! Integration tests of the `delivery-relay` lock session (FR-537, AC-615)
//! against a real PostgreSQL. Run only with `DATABASE_URL`:
//!
//! `DATABASE_URL=postgres://postgres:postgres@localhost:5432/postgres cargo test --test outbox_relay_lock`
//!
//! Liveness is an ownership check in `pg_locks`, so these tests cover the
//! held, terminated, released and half-open cases.

use std::sync::Arc;
use std::sync::atomic::{AtomicBool, Ordering};
use std::time::{Duration, Instant};

use sqlx::Connection as _;
use sqlx::postgres::PgConnection;
use tokio::io::{AsyncReadExt as _, AsyncWriteExt as _};
use tokio::net::{TcpListener, TcpStream};

use delivery::infrastructure::outbox::{
    LOCK_CHECK_TIMEOUT, PgOutbox, RELAY_LOCK_KEY, advisory_lock_parts, holds_advisory_lock,
};
use delivery::pkg::config::DatabaseConfig;

fn database() -> Option<DatabaseConfig> {
    let url = std::env::var("DATABASE_URL")
        .ok()
        .filter(|v| !v.is_empty())?;
    Some(DatabaseConfig {
        url,
        max_conns: 4,
        ..DatabaseConfig::default()
    })
}

/// The tests share the relay lock key: run them one at a time.
static SERIAL: tokio::sync::Mutex<()> = tokio::sync::Mutex::const_new(());

/// A lost session must be noticed well before a hung check would end.
const DETECTION: Duration = Duration::from_secs(1);

async fn acquire(outbox: &PgOutbox) {
    let deadline = Instant::now() + Duration::from_secs(30);
    while !outbox.try_acquire_lock().await.expect("lock") {
        assert!(Instant::now() < deadline, "lock not acquired");
        tokio::time::sleep(Duration::from_millis(100)).await;
    }
}

/// Terminates the backends holding the relay lock; returns how many.
async fn terminate_lock_holders(url: &str) -> i64 {
    let (classid, objid) = advisory_lock_parts(RELAY_LOCK_KEY);
    let mut admin = PgConnection::connect(url).await.expect("admin");
    sqlx::query_scalar(
        "SELECT count(pg_terminate_backend(pid)) FROM pg_locks
         WHERE locktype = 'advisory' AND granted AND pid <> pg_backend_pid()
           AND classid = $1::bigint::oid AND objid = $2::bigint::oid AND objsubid = 1",
    )
    .bind(i64::from(classid))
    .bind(i64::from(objid))
    .fetch_one(&mut admin)
    .await
    .expect("terminate lock session")
}

/// The check is about this session's ownership, not the lock's existence:
/// a lock held by another session is `false`, including negative keys.
#[tokio::test(flavor = "multi_thread")]
async fn ownership_check_sees_only_this_session() {
    let Some(database) = database() else { return };
    let _serial = SERIAL.lock().await;
    let key = -0x0123_4567_89ab_cdef_i64;
    let mut owner = PgConnection::connect(&database.url).await.expect("owner");
    let mut other = PgConnection::connect(&database.url).await.expect("other");

    assert!(!holds_advisory_lock(&mut owner, key).await.expect("check"));
    let acquired: bool = sqlx::query_scalar("SELECT pg_try_advisory_lock($1)")
        .bind(key)
        .fetch_one(&mut owner)
        .await
        .expect("lock");
    assert!(acquired);
    assert!(holds_advisory_lock(&mut owner, key).await.expect("check"));
    assert!(
        !holds_advisory_lock(&mut other, key).await.expect("check"),
        "another session holding the key is not ownership"
    );
    assert!(
        !holds_advisory_lock(&mut owner, key ^ 1)
            .await
            .expect("check"),
        "a different key is not ownership"
    );

    let _: bool = sqlx::query_scalar("SELECT pg_advisory_unlock($1)")
        .bind(key)
        .fetch_one(&mut owner)
        .await
        .expect("unlock");
    assert!(!holds_advisory_lock(&mut owner, key).await.expect("check"));
}

/// Held → `true`; a standby without the lock is `false`.
#[tokio::test(flavor = "multi_thread")]
async fn held_lock_is_alive_and_standby_is_not() {
    let Some(database) = database() else { return };
    let _serial = SERIAL.lock().await;
    let active = PgOutbox::connect(&database).await.expect("active");
    let standby = PgOutbox::connect(&database).await.expect("standby");
    assert!(!active.lock_alive().await, "no lock before acquiring");

    acquire(&active).await;
    assert!(active.lock_alive().await);
    assert!(!standby.try_acquire_lock().await.expect("standby"));
    assert!(!standby.lock_alive().await);
    active.release_lock().await;
}

/// `pg_terminate_backend` → `false` quickly, it stays lost until re-acquired,
/// and the standby takes the lock over.
#[tokio::test(flavor = "multi_thread")]
async fn terminated_lock_session_is_detected_quickly() {
    let Some(database) = database() else { return };
    let _serial = SERIAL.lock().await;
    let active = PgOutbox::connect(&database).await.expect("active");
    let standby = PgOutbox::connect(&database).await.expect("standby");
    acquire(&active).await;
    assert!(active.lock_alive().await);

    assert!(terminate_lock_holders(&database.url).await >= 1);

    let started = Instant::now();
    assert!(!active.lock_alive().await, "a lost session is detected");
    assert!(
        started.elapsed() < DETECTION,
        "detected in {:?}",
        started.elapsed()
    );
    assert!(
        !active.lock_alive().await,
        "and stays lost until re-acquired"
    );
    assert!(standby.try_acquire_lock().await.expect("failover"));
    assert!(standby.lock_alive().await);
    standby.release_lock().await;
    active.release_lock().await;
}

/// Released → `false`; the lock is free for the standby.
#[tokio::test(flavor = "multi_thread")]
async fn released_lock_is_not_alive() {
    let Some(database) = database() else { return };
    let _serial = SERIAL.lock().await;
    let active = PgOutbox::connect(&database).await.expect("active");
    let standby = PgOutbox::connect(&database).await.expect("standby");
    acquire(&active).await;

    active.release_lock().await;

    assert!(!active.lock_alive().await);
    assert!(
        standby
            .try_acquire_lock()
            .await
            .expect("free after release")
    );
    standby.release_lock().await;
}

/// TCP proxy that can stop forwarding without closing either side: the
/// client sees a half-open connection (writes are buffered, nothing returns).
struct FreezingProxy {
    addr: std::net::SocketAddr,
    frozen: Arc<AtomicBool>,
}

impl FreezingProxy {
    async fn start(upstream: String) -> Self {
        let listener = TcpListener::bind("127.0.0.1:0").await.expect("proxy");
        let addr = listener.local_addr().expect("proxy addr");
        let frozen = Arc::new(AtomicBool::new(false));
        let gate = Arc::clone(&frozen);
        tokio::spawn(async move {
            while let Ok((client, _)) = listener.accept().await {
                let Ok(server) = TcpStream::connect(&upstream).await else {
                    continue;
                };
                let (client_read, client_write) = client.into_split();
                let (server_read, server_write) = server.into_split();
                tokio::spawn(pump(client_read, server_write, Arc::clone(&gate)));
                tokio::spawn(pump(server_read, client_write, Arc::clone(&gate)));
            }
        });
        Self { addr, frozen }
    }

    fn freeze(&self) {
        self.frozen.store(true, Ordering::SeqCst);
    }
}

async fn pump(
    mut from: tokio::net::tcp::OwnedReadHalf,
    mut to: tokio::net::tcp::OwnedWriteHalf,
    frozen: Arc<AtomicBool>,
) {
    let mut buffer = [0_u8; 8192];
    loop {
        let Ok(read) = from.read(&mut buffer).await else {
            return;
        };
        if read == 0 {
            return;
        }
        if frozen.load(Ordering::SeqCst) {
            // Hold both halves open and forward nothing.
            std::future::pending::<()>().await;
        }
        if to.write_all(&buffer[..read]).await.is_err() {
            return;
        }
    }
}

/// `scheme://user:pass@host:port/db?...` → the same URL via `addr`.
fn through(url: &str, addr: std::net::SocketAddr) -> (String, String) {
    let at = url.rfind('@').expect("url with credentials");
    let rest = &url[at + 1..];
    let end = rest.find(['/', '?']).unwrap_or(rest.len());
    let upstream = rest[..end].to_owned();
    (format!("{}@{addr}{}", &url[..at], &rest[end..]), upstream)
}

/// A half-open lock connection cannot hang the check: it gives up after
/// `LOCK_CHECK_TIMEOUT` and the lock counts as lost.
#[tokio::test(flavor = "multi_thread")]
async fn half_open_lock_connection_does_not_hang_the_check() {
    let Some(database) = database() else { return };
    let _serial = SERIAL.lock().await;
    let (_, upstream) = through(&database.url, "127.0.0.1:1".parse().expect("addr"));
    let upstream = upstream.replace("localhost", "127.0.0.1");
    let proxy = FreezingProxy::start(upstream).await;
    let (proxied, _) = through(&database.url, proxy.addr);
    let active = PgOutbox::connect(&DatabaseConfig {
        url: proxied,
        max_conns: 2,
        ..DatabaseConfig::default()
    })
    .await
    .expect("active");
    acquire(&active).await;
    assert!(active.lock_alive().await);

    proxy.freeze();

    let started = Instant::now();
    assert!(!active.lock_alive().await, "a hung check is a lost lock");
    let took = started.elapsed();
    assert!(
        took >= LOCK_CHECK_TIMEOUT && took < LOCK_CHECK_TIMEOUT + DETECTION,
        "gave up after {took:?}"
    );
    // The frozen session still holds the lock on the server until its
    // keepalives expire; free it for the other tests.
    assert!(terminate_lock_holders(&database.url).await >= 1);
}

use std::time::Instant;

use tokio::sync::oneshot;

use super::health::tests::get;
use super::*;
use crate::di::HttpWorker;

fn settings(readiness_delay: Duration, drain_timeout: Duration) -> Settings {
    Settings {
        management_addr: ([127, 0, 0, 1], 0).into(),
        readiness_delay,
        drain_timeout,
    }
}

/// Останавливается по сигналу после `drain` и сообщает момент остановки.
struct DrainingWorker {
    drain: Duration,
    stopped_at: Option<oneshot::Sender<Instant>>,
}

impl DrainingWorker {
    fn new(drain: Duration) -> Self {
        Self {
            drain,
            stopped_at: None,
        }
    }
}

impl Worker for DrainingWorker {
    async fn run(self, mut stop: watch::Receiver<bool>) -> io::Result<()> {
        wait_for_stop(&mut stop).await;
        if let Some(stopped_at) = self.stopped_at {
            let _ = stopped_at.send(Instant::now());
        }
        tokio::time::sleep(self.drain).await;
        Ok(())
    }
}

/// Игнорирует сигнал остановки.
struct StuckWorker;

impl Worker for StuckWorker {
    async fn run(self, _stop: watch::Receiver<bool>) -> io::Result<()> {
        std::future::pending().await
    }
}

/// Завершается сам, не дожидаясь сигнала.
struct ExitingWorker(Option<io::Error>);

impl Worker for ExitingWorker {
    async fn run(self, _stop: watch::Receiver<bool>) -> io::Result<()> {
        match self.0 {
            Some(error) => Err(error),
            None => Ok(()),
        }
    }
}

async fn wait_until(condition: impl Fn() -> bool) {
    tokio::time::timeout(Duration::from_secs(2), async {
        while !condition() {
            tokio::time::sleep(Duration::from_millis(5)).await;
        }
    })
    .await
    .expect("condition reached");
}

#[tokio::test]
async fn ready_after_start_and_graceful_on_stop() {
    let lifecycle = Lifecycle::bind(settings(Duration::from_millis(200), Duration::from_secs(1)))
        .await
        .expect("bind");
    let health = lifecycle.health();
    assert!(!health.ready());

    let (stop_tx, stop_rx) = oneshot::channel::<()>();
    let run = tokio::spawn(lifecycle.run_until(
        DrainingWorker::new(Duration::from_millis(10)),
        async {
            let _ = stop_rx.await;
        },
    ));
    wait_until(|| health.ready()).await;

    stop_tx.send(()).expect("send stop");
    wait_until(|| health.is_shutting_down()).await;
    assert!(!health.ready());
    assert!(health.is_started());
    assert!(!run.is_finished());

    run.await.expect("join").expect("graceful shutdown");
    assert!(!health.ready());
}

#[tokio::test]
async fn honors_readiness_delay_before_stop() {
    let delay = Duration::from_millis(300);
    let lifecycle = Lifecycle::bind(settings(delay, Duration::from_secs(1)))
        .await
        .expect("bind");
    let health = lifecycle.health();
    let (stopped_tx, stopped_rx) = oneshot::channel();
    let worker = DrainingWorker {
        drain: Duration::ZERO,
        stopped_at: Some(stopped_tx),
    };

    let (stop_tx, stop_rx) = oneshot::channel::<()>();
    let run = tokio::spawn(lifecycle.run_until(worker, async {
        let _ = stop_rx.await;
    }));
    wait_until(|| health.ready()).await;

    let requested = Instant::now();
    stop_tx.send(()).expect("send stop");
    let stopped = stopped_rx.await.expect("worker stopped");
    assert!(stopped.duration_since(requested) >= delay);
    run.await.expect("join").expect("graceful shutdown");
}

#[tokio::test]
async fn fails_when_drain_timeout_exceeded() {
    let drain = Duration::from_millis(100);
    let lifecycle = Lifecycle::bind(settings(Duration::ZERO, drain))
        .await
        .expect("bind");

    let started = Instant::now();
    let result = lifecycle.run_until(StuckWorker, async {}).await;

    assert!(matches!(result, Err(LifecycleError::DrainTimeout(timeout)) if timeout == drain));
    assert!(started.elapsed() < Duration::from_secs(1));
}

#[tokio::test]
async fn worker_bind_failure_never_becomes_ready() {
    let occupied = tokio::net::TcpListener::bind("127.0.0.1:0")
        .await
        .expect("occupy port");
    let lifecycle = Lifecycle::bind(settings(Duration::ZERO, Duration::from_secs(1)))
        .await
        .expect("bind");
    let health = lifecycle.health();
    let management = lifecycle.management_addr();

    let worker = HttpWorker::bind(occupied.local_addr().expect("addr")).await;

    assert!(worker.is_err());
    assert!(!health.is_started());
    assert_eq!(get(management, "/health/startup").await.status, 503);
    drop(lifecycle);
}

#[tokio::test]
async fn worker_failing_at_start_is_error() {
    let lifecycle = Lifecycle::bind(settings(Duration::ZERO, Duration::from_secs(1)))
        .await
        .expect("bind");
    let health = lifecycle.health();

    let result = lifecycle
        .run_until(
            ExitingWorker(Some(io::Error::other("boom"))),
            std::future::pending(),
        )
        .await;

    assert!(matches!(result, Err(LifecycleError::WorkerStopped(reason)) if reason == "boom"));
    assert!(!health.ready());
}

#[tokio::test]
async fn worker_stopping_on_its_own_is_error() {
    let lifecycle = Lifecycle::bind(settings(Duration::ZERO, Duration::from_secs(1)))
        .await
        .expect("bind");

    let result = lifecycle
        .run_until(ExitingWorker(None), std::future::pending())
        .await;

    assert!(matches!(result, Err(LifecycleError::WorkerStopped(_))));
}

#[tokio::test]
async fn worker_exiting_during_readiness_delay_is_error() {
    let lifecycle = Lifecycle::bind(settings(Duration::from_secs(5), Duration::from_secs(1)))
        .await
        .expect("bind");

    /// Завершается вскоре после старта, не дожидаясь сигнала.
    struct ShortWorker;
    impl Worker for ShortWorker {
        async fn run(self, _stop: watch::Receiver<bool>) -> io::Result<()> {
            tokio::time::sleep(Duration::from_millis(100)).await;
            Ok(())
        }
    }

    let started = Instant::now();
    let result = lifecycle.run_until(ShortWorker, async {}).await;

    assert!(matches!(result, Err(LifecycleError::WorkerStopped(_))));
    assert!(started.elapsed() < Duration::from_secs(2));
}

#[tokio::test]
async fn health_endpoints_follow_lifecycle() {
    let lifecycle = Lifecycle::bind(settings(Duration::from_millis(500), Duration::from_secs(1)))
        .await
        .expect("bind");
    let health = lifecycle.health();
    let addr = lifecycle.management_addr();

    assert_eq!(get(addr, "/health/startup").await.status, 503);
    assert_eq!(get(addr, "/health/ready").await.status, 503);
    assert_eq!(get(addr, "/health/live").await.status, 200);

    let (stop_tx, stop_rx) = oneshot::channel::<()>();
    let run = tokio::spawn(
        lifecycle.run_until(DrainingWorker::new(Duration::ZERO), async {
            let _ = stop_rx.await;
        }),
    );
    wait_until(|| health.ready()).await;
    assert_eq!(get(addr, "/health/startup").await.status, 200);
    assert_eq!(get(addr, "/health/ready").await.status, 200);

    stop_tx.send(()).expect("send stop");
    wait_until(|| health.is_shutting_down()).await;
    let ready = get(addr, "/health/ready").await;
    assert_eq!(ready.status, 503);
    assert_eq!(
        ready.body,
        r#"{"status":"unavailable","reason":"shutting_down"}"#
    );
    assert_eq!(get(addr, "/health/live").await.status, 200);
    assert_eq!(get(addr, "/health/unknown").await.status, 404);

    run.await.expect("join").expect("graceful shutdown");
    assert!(tokio::net::TcpStream::connect(addr).await.is_err());
}

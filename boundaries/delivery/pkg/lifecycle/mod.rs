//! Runtime contract процесса `delivery` (docs/cloud-native-runtime.md,
//! ADR 17): health state, технический порт, обработка `SIGTERM`/`SIGINT`,
//! readiness-propagation delay и drain. Бизнес-код от него не зависит: worker
//! получает только сигнал остановки.
//!
//! Повторный сигнал во время shutdown последовательность не перезапускает.
//! tokio не умеет вернуть обработчик сигнала по умолчанию, поэтому повторный
//! `SIGTERM`/`SIGINT` ускоряет завершение явно: worker прерывается, технический
//! порт закрывается, [`Lifecycle::run`] возвращает [`LifecycleError::Forced`]
//! (non-zero exit).

mod health;

use std::fmt;
use std::future::Future;
use std::io;
use std::net::SocketAddr;
use std::sync::Arc;
use std::time::Duration;

use tokio::signal::unix::{Signal, SignalKind, signal};
use tokio::sync::watch;
use tokio::task::{JoinError, JoinHandle};

pub use health::Health;
use health::ManagementServer;

/// Параметры runtime contract.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct Settings {
    /// Адрес технического порта с `/health/*`.
    pub management_addr: SocketAddr,
    /// Ожидание распространения not ready до остановки приёма работы.
    pub readiness_delay: Duration,
    /// Deadline на завершение начатой работы.
    pub drain_timeout: Duration,
}

/// Единица работы процесса, которой владеет composition root: HTTP/gRPC
/// server, consumer или Temporal worker.
///
/// Ресурсы (например, listener) захватываются до [`Worker::run`] в
/// конструкторе worker: ошибка там прерывает startup до ready.
pub trait Worker: Send + 'static {
    /// Обслуживает работу, пока `stop` не станет `true`, затем прекращает приём
    /// новой работы, дожидается начатой и возвращает `Ok(())`. Deadline drain
    /// соблюдает [`Lifecycle`]: по его истечении future прерывается.
    fn run(
        self,
        stop: watch::Receiver<bool>,
    ) -> impl Future<Output = io::Result<()>> + Send + 'static;
}

/// Ждёт сигнала остановки worker. Закрытый канал тоже означает остановку.
pub async fn wait_for_stop(stop: &mut watch::Receiver<bool>) {
    let _ = stop.wait_for(|stopped| *stopped).await;
}

/// Причина non-zero exit.
#[derive(Debug)]
pub enum LifecycleError {
    /// Не удалось установить обработчики `SIGTERM`/`SIGINT`.
    Signals(io::Error),
    /// Worker завершился до начала shutdown или во время readiness delay.
    WorkerStopped(String),
    /// Worker вернул ошибку во время drain.
    Worker(io::Error),
    /// Начатая работа не завершилась в пределах `drain_timeout`.
    DrainTimeout(Duration),
    /// Повторный сигнал прервал graceful shutdown.
    Forced,
}

impl fmt::Display for LifecycleError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::Signals(error) => write!(f, "install signal handlers: {error}"),
            Self::WorkerStopped(reason) => write!(f, "worker stopped before shutdown: {reason}"),
            Self::Worker(error) => write!(f, "worker shutdown: {error}"),
            Self::DrainTimeout(timeout) => write!(
                f,
                "drain timeout {timeout:?} exceeded, in-flight work aborted"
            ),
            Self::Forced => write!(f, "shutdown forced by repeated signal"),
        }
    }
}

impl std::error::Error for LifecycleError {
    fn source(&self) -> Option<&(dyn std::error::Error + 'static)> {
        match self {
            Self::Signals(error) | Self::Worker(error) => Some(error),
            _ => None,
        }
    }
}

/// Lifecycle процесса: технический порт → worker → ready →
/// `SIGTERM`/`SIGINT` → not ready → readiness delay → stop → drain → закрытие
/// технического порта.
pub struct Lifecycle {
    settings: Settings,
    health: Arc<Health>,
    management: ManagementServer,
}

impl Lifecycle {
    /// Открывает технический порт. До [`Lifecycle::run`] startup и readiness
    /// отвечают 503 `initializing`, liveness — 200.
    pub async fn bind(settings: Settings) -> io::Result<Self> {
        let health = Arc::new(Health::default());
        let management =
            ManagementServer::bind(settings.management_addr, Arc::clone(&health)).await?;
        println!("management listening on {}", management.local_addr());
        Ok(Self {
            settings,
            health,
            management,
        })
    }

    #[must_use]
    pub fn health(&self) -> Arc<Health> {
        Arc::clone(&self.health)
    }

    /// Фактический адрес технического порта (важно при порте `0`).
    #[must_use]
    pub fn management_addr(&self) -> SocketAddr {
        self.management.local_addr()
    }

    /// Запускает worker и ведёт его до штатного shutdown по
    /// `SIGTERM`/`SIGINT`. `Ok(())` — штатный shutdown, любая ошибка — non-zero
    /// exit.
    pub async fn run<W: Worker>(self, worker: W) -> Result<(), LifecycleError> {
        self.run_until(worker, std::future::pending()).await
    }

    /// Как [`Lifecycle::run`], но shutdown начинается также по завершении
    /// `stop` — для тестов и внешнего управления.
    pub async fn run_until<W, F>(self, worker: W, stop: F) -> Result<(), LifecycleError>
    where
        W: Worker,
        F: Future<Output = ()>,
    {
        let Self {
            settings,
            health,
            management,
        } = self;
        let result = drive(settings, &health, worker, stop).await;
        // Технический порт закрывается последним: до этого probes видят
        // live=200 и ready=503.
        management.shutdown().await;
        result
    }
}

async fn drive<W, F>(
    settings: Settings,
    health: &Health,
    worker: W,
    stop: F,
) -> Result<(), LifecycleError>
where
    W: Worker,
    F: Future<Output = ()>,
{
    // delivery запускается под tini, сигналы обрабатываются явно.
    let mut signals = ShutdownSignals::install().map_err(LifecycleError::Signals)?;
    let (stop_tx, stop_rx) = watch::channel(false);
    let mut worker_task = tokio::spawn(worker.run(stop_rx));
    health.mark_started();

    let mut stop = std::pin::pin!(stop);
    tokio::select! {
        result = &mut worker_task => {
            health.mark_shutting_down();
            return Err(stopped_unexpectedly(result));
        }
        () = signals.recv() => {}
        () = &mut stop => {}
    }

    health.mark_shutting_down();
    println!(
        "shutting down: readiness delay {:?}, drain timeout {:?}",
        settings.readiness_delay, settings.drain_timeout
    );

    tokio::select! {
        result = &mut worker_task => return Err(stopped_unexpectedly(result)),
        () = tokio::time::sleep(settings.readiness_delay) => {}
        () = signals.recv() => return Err(abort(worker_task, LifecycleError::Forced).await),
    }

    let _ = stop_tx.send(true);
    tokio::select! {
        drained = tokio::time::timeout(settings.drain_timeout, &mut worker_task) => match drained {
            Ok(Ok(Ok(()))) => Ok(()),
            Ok(Ok(Err(error))) => Err(LifecycleError::Worker(error)),
            Ok(Err(error)) => Err(LifecycleError::Worker(io::Error::other(error.to_string()))),
            Err(_) => Err(abort(worker_task, LifecycleError::DrainTimeout(settings.drain_timeout)).await),
        },
        () = signals.recv() => Err(abort(worker_task, LifecycleError::Forced).await),
    }
}

/// Прерывает worker и дожидается освобождения его ресурсов (listener,
/// соединения в `JoinSet`).
async fn abort(worker_task: JoinHandle<io::Result<()>>, error: LifecycleError) -> LifecycleError {
    worker_task.abort();
    let _ = worker_task.await;
    error
}

fn stopped_unexpectedly(result: Result<io::Result<()>, JoinError>) -> LifecycleError {
    let reason = match result {
        Ok(Ok(())) => "no error reported".to_owned(),
        Ok(Err(error)) => error.to_string(),
        Err(error) => error.to_string(),
    };
    LifecycleError::WorkerStopped(reason)
}

struct ShutdownSignals {
    terminate: Signal,
    interrupt: Signal,
}

impl ShutdownSignals {
    fn install() -> io::Result<Self> {
        Ok(Self {
            terminate: signal(SignalKind::terminate())?,
            interrupt: signal(SignalKind::interrupt())?,
        })
    }

    async fn recv(&mut self) {
        tokio::select! {
            _ = self.terminate.recv() => {}
            _ = self.interrupt.recv() => {}
        }
    }
}

#[cfg(test)]
mod tests {
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
        let lifecycle =
            Lifecycle::bind(settings(Duration::from_millis(200), Duration::from_secs(1)))
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
        let lifecycle =
            Lifecycle::bind(settings(Duration::from_millis(500), Duration::from_secs(1)))
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
}

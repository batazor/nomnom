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
        tracing::info!(operation = "management.listen", address = %management.local_addr(), "management listening");
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
    tracing::info!(
        operation = "shutdown",
        readiness_delay = ?settings.readiness_delay,
        drain_timeout = ?settings.drain_timeout,
        "shutting down"
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
mod tests;

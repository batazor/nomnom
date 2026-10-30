//! Composition root процесса `delivery-worker` (`cmd/delivery-worker`,
//! FR-708): Temporal worker Assignment Workflow и его Activities на task
//! queue `delivery`.
//!
//! Activities вызывают use cases Advance Assignment и Expire Delivery Offer
//! над тем же PostgreSQL, что и процесс `delivery`, с теми же метриками
//! Outbox ([`ObservedStore`]). Процессу не нужны gRPC API, SpiceDB, Valkey и
//! Kafka: шаги назначения идут без subject и проверки прав. Схему мигрирует
//! процесс `delivery`; worker только подключается.
//!
//! SDK исполняет workflows на не-`Send` futures, поэтому worker живёт в
//! собственном потоке с current-thread runtime; [`compose`] ждёт, пока он
//! подключится к Temporal (fail fast), а lifecycle видит только сигнал
//! остановки и завершение потока.

use std::error::Error;
use std::io;
use std::sync::Arc;
use std::sync::mpsc as std_mpsc;

use temporalio_client::{Client, ClientOptions, Connection, ConnectionOptions};
use temporalio_sdk::{Runtime, Worker as TemporalSdkWorker, WorkerOptions};
use tokio::sync::{oneshot, watch};
use url::Url;

use crate::infrastructure::ids::UuidIdGenerator;
use crate::infrastructure::locator::StoredCourierLocator;
use crate::infrastructure::repository::Store;
use crate::infrastructure::temporal::{
    AssignmentActivities, AssignmentSteps, AssignmentWorkflow, Step, TASK_QUEUE,
};
use crate::pkg::config::{TemporalConfig, WorkerConfig};
use crate::pkg::lifecycle::Worker;
use crate::pkg::telemetry::usecase::UseCaseTelemetry;
use crate::pkg::telemetry::{self, Telemetry};
use crate::pkg::unit_of_work::ScopedUnitOfWork;
use crate::slices::delivery::applications::error::DeliveryApplicationError;
use crate::slices::delivery::applications::policy::AssignmentPolicy;
use crate::slices::delivery::applications::{
    assignment::advance_assignment, offer::expire_delivery_offer,
};
use crate::slices::delivery::infrastructure::DeliveryIntegrationEvents;
use crate::slices::delivery::infrastructure::observed_use_cases::{
    ADVANCE_ASSIGNMENT, EXPIRE_DELIVERY_OFFER, classify,
};

use super::infrastructure::{LOCAL_ASSIGNMENT_POLICY, ObservedStore, SystemClock, observed_outbox};

/// `service.name` процесса по умолчанию (`OTEL_SERVICE_NAME` переопределяет).
pub const SERVICE_NAME: &str = "delivery-worker";

/// Use cases шагов назначения над хранилищем процесса, каждый вызов — span
/// и `nomnom.usecase.duration`, как у use cases процесса `delivery`.
struct UseCaseSteps {
    store: ObservedStore,
    locator: StoredCourierLocator,
    ids: UuidIdGenerator,
    clock: SystemClock,
    telemetry: UseCaseTelemetry,
    policy: AssignmentPolicy,
}

impl AssignmentSteps for UseCaseSteps {
    fn advance(&self, delivery_id: String) -> Result<Step, DeliveryApplicationError> {
        self.telemetry.observe(ADVANCE_ASSIGNMENT, classify, || {
            self.advance_step(delivery_id)
        })
    }

    fn expire(&self, offer_id: String) -> Result<(), DeliveryApplicationError> {
        self.telemetry.observe(EXPIRE_DELIVERY_OFFER, classify, || {
            self.expire_step(offer_id)
        })
    }
}

impl UseCaseSteps {
    fn advance_step(&self, delivery_id: String) -> Result<Step, DeliveryApplicationError> {
        advance_assignment::Handler {
            unit_of_work: &ScopedUnitOfWork::new(&self.store),
            deliveries: &self.store,
            couriers: &self.store,
            offers: &self.store,
            offers_of: &self.store,
            outbox: &self.store,
            locator: &self.locator,
            ids: &self.ids,
            clock: &self.clock,
            policy: self.policy,
        }
        .execute(advance_assignment::Input { delivery_id })
        .map(Step::from)
    }

    fn expire_step(&self, offer_id: String) -> Result<(), DeliveryApplicationError> {
        expire_delivery_offer::Handler {
            unit_of_work: &ScopedUnitOfWork::new(&self.store),
            offers: &self.store,
            outbox: &self.store,
            clock: &self.clock,
        }
        .execute(expire_delivery_offer::Input { offer_id })
        .map(drop)
    }
}

/// Шаги назначения над хранилищем процесса: UUIDv7, локатор сохранённых
/// позиций и системные часы; `policy` — политика назначения процесса.
#[must_use]
pub fn assignment_steps(store: Arc<Store>, policy: AssignmentPolicy) -> Arc<dyn AssignmentSteps> {
    Arc::new(UseCaseSteps {
        store: ObservedStore::new(Arc::clone(&store) as _, observed_outbox()),
        locator: StoredCourierLocator::new(store),
        ids: UuidIdGenerator,
        clock: SystemClock,
        telemetry: UseCaseTelemetry::global(),
        policy,
    })
}

/// Процесс `delivery-worker`: Temporal worker и metrics pipeline.
pub struct WorkerProcess {
    worker: Option<TemporalWorker>,
    telemetry: Option<Telemetry>,
}

impl WorkerProcess {
    /// Worker процесса; вызывается один раз после открытия технического
    /// порта.
    pub fn worker(&mut self) -> Result<TemporalWorker, Box<dyn Error>> {
        self.worker
            .take()
            .ok_or_else(|| "temporal worker is already running".into())
    }

    /// Выгружает метрики после остановки worker.
    pub fn shutdown(&mut self) {
        if let Some(telemetry) = self.telemetry.take() {
            telemetry.shutdown();
        }
    }
}

/// Собирает процесс `delivery-worker`. Пустой или невалидный конфиг,
/// недоступные PostgreSQL или Temporal прерывают startup до открытия портов.
/// Вызывается внутри tokio runtime: OTLP exporter держит соединение на нём.
pub fn compose(config: &WorkerConfig) -> Result<WorkerProcess, Box<dyn Error>> {
    let telemetry = telemetry::start(SERVICE_NAME)?;
    let store = Arc::new(
        Store::connect(&config.database)?.with_outbox_mapper(Arc::new(DeliveryIntegrationEvents)),
    );
    let worker = TemporalWorker::start(
        &config.temporal,
        assignment_steps(store, LOCAL_ASSIGNMENT_POLICY),
    )?;
    Ok(WorkerProcess {
        worker: Some(worker),
        telemetry: Some(telemetry),
    })
}

/// Temporal worker в собственном потоке: остановка — `initiate_shutdown`
/// SDK, после которой начатые задачи доводятся до конца в пределах drain.
pub struct TemporalWorker {
    shutdown: Box<dyn Fn() + Send + Sync>,
    done: oneshot::Receiver<Result<(), String>>,
}

impl TemporalWorker {
    /// Подключается к Temporal и начинает опрашивать task queue `delivery`;
    /// недоступный frontend — ошибка.
    pub fn start(config: &TemporalConfig, steps: Arc<dyn AssignmentSteps>) -> Result<Self, String> {
        let target = Url::parse(&format!("http://{}", config.host_port))
            .map_err(|error| format!("temporal host_port {}: {error}", config.host_port))?;
        let connection = ConnectionOptions::new(target)
            .identity(format!("delivery-worker@{}", std::process::id()))
            .connect_timeout(config.connect_timeout)
            .build();
        let namespace = config.namespace.clone();
        let (ready_tx, ready_rx) = std_mpsc::sync_channel(1);
        let (done_tx, done) = oneshot::channel();
        std::thread::Builder::new()
            .name("temporal-worker".to_owned())
            .spawn(move || {
                let runtime = match tokio::runtime::Builder::new_current_thread()
                    .enable_all()
                    .build()
                {
                    Ok(runtime) => runtime,
                    Err(error) => {
                        let _ = ready_tx.send(Err(format!("temporal worker runtime: {error}")));
                        return;
                    }
                };
                let local = tokio::task::LocalSet::new();
                let result = local.block_on(&runtime, async move {
                    let mut worker = match connect(connection, namespace, steps).await {
                        Ok(worker) => worker,
                        Err(error) => {
                            let _ = ready_tx.send(Err(error));
                            return Ok(());
                        }
                    };
                    let handle: Box<dyn Fn() + Send + Sync> = Box::new(worker.shutdown_handle());
                    if ready_tx.send(Ok(handle)).is_err() {
                        return Ok(());
                    }
                    worker
                        .run()
                        .await
                        .map_err(|error| format!("temporal worker: {error}"))
                });
                let _ = done_tx.send(result);
            })
            .map_err(|error| format!("start temporal worker thread: {error}"))?;
        let shutdown = ready_rx
            .recv()
            .map_err(|_| "temporal worker thread stopped before connecting".to_owned())??;
        Ok(Self { shutdown, done })
    }
}

async fn connect(
    connection: ConnectionOptions,
    namespace: String,
    steps: Arc<dyn AssignmentSteps>,
) -> Result<TemporalSdkWorker, String> {
    let runtime = Runtime::from_current_tokio(Default::default())
        .map_err(|error| format!("temporal runtime: {error}"))?;
    let connection = Connection::connect(connection)
        .await
        .map_err(|error| format!("connect temporal: {error}"))?;
    let client = Client::new(connection, ClientOptions::new(namespace).build())
        .map_err(|error| format!("temporal client: {error}"))?;
    let options = WorkerOptions::new(TASK_QUEUE)
        .register_workflow::<AssignmentWorkflow>()
        .map_err(|error| format!("register assignment workflow: {error}"))?
        .register_activities(AssignmentActivities::new(steps))
        .build();
    TemporalSdkWorker::new(&runtime, client, options)
        .map_err(|error| format!("temporal worker: {error}"))
}

impl Worker for TemporalWorker {
    async fn run(self, mut stop: watch::Receiver<bool>) -> io::Result<()> {
        let Self { shutdown, mut done } = self;
        tokio::select! {
            finished = &mut done => {
                // Worker завершился сам: lifecycle сочтёт это ошибкой.
                return Err(io::Error::other(finished_error(finished)));
            }
            () = crate::pkg::lifecycle::wait_for_stop(&mut stop) => {}
        }
        shutdown();
        match done.await {
            Ok(Ok(())) => Ok(()),
            finished => Err(io::Error::other(finished_error(finished))),
        }
    }
}

fn finished_error(finished: Result<Result<(), String>, oneshot::error::RecvError>) -> String {
    match finished {
        Ok(Ok(())) => "temporal worker stopped".to_owned(),
        Ok(Err(error)) => error,
        Err(_) => "temporal worker thread panicked".to_owned(),
    }
}

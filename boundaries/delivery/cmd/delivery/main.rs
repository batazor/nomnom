use std::error::Error;
use std::net::SocketAddr;
use std::process::ExitCode;
use std::sync::Arc;
use std::time::{Duration, Instant};

use delivery::di;
use delivery::infrastructure::grpc::{GrpcWorker, subject_resolver};
use delivery::infrastructure::reconcile::{Reconciler, Schedule};
use delivery::infrastructure::spicedb::{RelationshipGraph, SpiceDbRelationships};
use delivery::pkg::config;
use delivery::pkg::lifecycle::{Lifecycle, Settings};

/// Первый проход reconciliation вскоре после старта.
const FIRST_RECONCILIATION: Duration = Duration::from_secs(10);

#[tokio::main]
async fn main() -> ExitCode {
    match run().await {
        Ok(()) => ExitCode::SUCCESS,
        Err(error) => {
            eprintln!("delivery: {error}");
            ExitCode::FAILURE
        }
    }
}

/// `Ok(())` — штатный shutdown (exit 0), любая ошибка — non-zero exit.
async fn run() -> Result<(), Box<dyn Error>> {
    // Невалидная конфигурация завершает процесс до открытия портов.
    let config = config::load().map_err(|error| format!("load config: {error}"))?;
    // Невалидный JWKS или allowlist service identity тоже прерывает startup.
    let subjects = subject_resolver(&config.identity)?;
    // SpiceDB обязателен: проверки прав, запись relationships и reconciliation.
    let spicedb = di::spicedb(&config.spicedb)?;
    let graph: Arc<dyn RelationshipGraph> = Arc::new(spicedb.clone());
    // PostgreSQL обязателен: пустой DATABASE__URL или недоступная база
    // прерывают startup. Revisions записей relationships хранятся с Courier и
    // Delivery; checks читают их из того же хранилища (FR-447).
    let store: Arc<dyn di::ProcessStore> = Arc::new(di::postgres(&config.database)?);
    let process = di::build_process(
        Arc::clone(&store),
        Box::new(di::spicedb_authorization(spicedb, store)),
        Box::new(SpiceDbRelationships::new(Arc::clone(&graph))),
    );
    let reconcilers = vec![
        Reconciler::couriers(
            Arc::clone(&graph),
            Arc::clone(&process.store) as _,
            config.reconciler.grace,
            config.reconciler.page_size,
            Arc::new(Instant::now),
        ),
        Reconciler::deliveries(
            graph,
            Arc::clone(&process.store) as _,
            config.reconciler.grace,
            config.reconciler.page_size,
            Arc::new(Instant::now),
        ),
    ];
    let module = Arc::new(process.module);

    let lifecycle = Lifecycle::bind(Settings {
        management_addr: SocketAddr::from(([0, 0, 0, 0], config.management.port)),
        readiness_delay: config.shutdown.readiness_delay,
        drain_timeout: config.shutdown.drain_timeout,
    })
    .await
    .map_err(|error| format!("listen management :{}: {error}", config.management.port))?;

    let worker = GrpcWorker::bind(
        SocketAddr::from(([0, 0, 0, 0], config.server.port)),
        module,
        subjects,
    )
    .map_err(|error| format!("listen :{}: {error}", config.server.port))?;

    let worker = di::ReconcilingWorker::new(
        worker,
        reconcilers,
        Schedule {
            first_pass: FIRST_RECONCILIATION.min(config.reconciler.interval),
            interval: config.reconciler.interval,
        },
    );
    lifecycle.run(worker).await?;
    Ok(())
}

//! Composition root процесса `delivery` (`cmd/delivery`): gRPC API и фоновый
//! reconciliation relationships.
//!
//! [`compose`] выбирает реализации портов и открывает общие клиенты процесса
//! (fail fast: SpiceDB, PostgreSQL, service identity), собирает граф use cases
//! ([`super::service`]) и итоговый [`ServerModule`]. Worker процесса создаёт
//! provider [`DeliveryWorkerProvider`] уже после открытия технического порта:
//! бизнес-порт открывается последним (ADR 17).
//!
//! Время жизни: SpiceDB-клиент, пул PostgreSQL и resolver identity живут
//! весь процесс и принадлежат модулю; транзакция — один use case (Unit of
//! Work хранилища); reconcilers создаются на worker и помнят orphan-кандидатов
//! между проходами.

use std::error::Error;
use std::io;
use std::net::SocketAddr;
use std::sync::Arc;
use std::time::{Duration, Instant};

use shaku::{Component, HasComponent, HasProvider, Interface, Module, Provider, module};

use crate::infrastructure::grpc::{GrpcWorker, SubjectResolver, subject_resolver};
use crate::infrastructure::memory::StaticCourierLocator;
use crate::infrastructure::postgres::PostgresStore;
use crate::infrastructure::reconcile::{Reconciler, Schedule};
use crate::infrastructure::spicedb::{
    RelationshipGraph, RepositoryRevisions, RevisionReader, SpiceDbAuthorization, SpiceDbClient,
    SpiceDbConfigError, SpiceDbRelationships,
};
use crate::pkg::config::{AppConfig, DatabaseConfig, SPICEDB_ENDPOINT_ENV, SpiceDbConfig};
use crate::slices::delivery::infrastructure::DeliveryIntegrationEvents;

use super::ReconcilingWorker;
use super::infrastructure::{InfrastructureModule, ProcessStore, SharedStore, infrastructure};
use super::service::{ServiceModule, service};

/// Первый проход reconciliation вскоре после старта.
const FIRST_RECONCILIATION: Duration = Duration::from_secs(10);

/// Клиент SpiceDB процесса: проверки прав, запись relationships и
/// reconciliation делят одно соединение. Пустой endpoint — ошибка startup.
pub fn spicedb(config: &SpiceDbConfig) -> Result<SpiceDbClient, SpiceDbConfigError> {
    if !config.enabled() {
        return Err(SpiceDbConfigError::missing_endpoint(SPICEDB_ENDPOINT_ENV));
    }
    SpiceDbClient::connect(config)
}

/// Хранилище процесса — PostgreSQL. Пустой `DATABASE__URL` или недоступная
/// база — ошибка startup (fail fast).
///
/// Domain Events с контрактом `nomnom/delivery/v1/events.proto` пишутся в
/// Outbox той же транзакцией (ADR 31); публикует их `delivery-relay`.
pub fn postgres(config: &DatabaseConfig) -> Result<PostgresStore, String> {
    PostgresStore::connect(config)
        .map(|store| store.with_outbox_mapper(Arc::new(DeliveryIntegrationEvents)))
}

/// SpiceDB-авторизация процесса: checks `courier:<id>` и `delivery:<id>`
/// видят собственные записи relationships по revision из `store` (FR-447).
#[must_use]
pub fn spicedb_authorization(
    client: SpiceDbClient,
    store: Arc<dyn ProcessStore>,
) -> SpiceDbAuthorization {
    SpiceDbAuthorization::new(Box::new(client))
        .with_revisions(Arc::new(RepositoryRevisions(store)) as Arc<dyn RevisionReader>)
}

/// gRPC-транспорт процесса: use cases, проверка вызывающего и бизнес-порт.
pub trait GrpcTransport: Interface {
    fn port(&self) -> u16;

    /// Открывает бизнес-порт; ошибка прерывает startup до ready.
    fn bind(&self) -> io::Result<GrpcWorker>;
}

#[derive(Component)]
#[shaku(interface = GrpcTransport)]
pub struct TonicTransport {
    use_cases: Arc<ServiceModule>,
    subjects: Arc<dyn SubjectResolver>,
    port: u16,
}

impl GrpcTransport for TonicTransport {
    fn port(&self) -> u16 {
        self.port
    }

    fn bind(&self) -> io::Result<GrpcWorker> {
        GrpcWorker::bind(
            SocketAddr::from(([0, 0, 0, 0], self.port)),
            Arc::clone(&self.use_cases),
            Arc::clone(&self.subjects),
        )
    }
}

/// Reconciliation relationships SpiceDB с business state хранилища (FR-445).
pub trait RelationshipReconciliation: Interface {
    /// Reconcilers `courier#self` и `delivery#courier` одного worker.
    fn reconcilers(&self) -> Vec<Reconciler>;
    fn schedule(&self) -> Schedule;
}

#[derive(Component)]
#[shaku(interface = RelationshipReconciliation)]
pub struct SpiceDbReconciliation {
    /// То же хранилище, что у портов слайсов.
    #[shaku(inject)]
    store: Arc<dyn SharedStore>,
    graph: Arc<dyn RelationshipGraph>,
    grace: Duration,
    page_size: u32,
    interval: Duration,
}

impl RelationshipReconciliation for SpiceDbReconciliation {
    fn reconcilers(&self) -> Vec<Reconciler> {
        vec![
            Reconciler::couriers(
                Arc::clone(&self.graph),
                self.store.shared() as _,
                self.grace,
                self.page_size,
                Arc::new(Instant::now),
            ),
            Reconciler::deliveries(
                Arc::clone(&self.graph),
                self.store.shared() as _,
                self.grace,
                self.page_size,
                Arc::new(Instant::now),
            ),
        ]
    }

    fn schedule(&self) -> Schedule {
        Schedule {
            first_pass: FIRST_RECONCILIATION.min(self.interval),
            interval: self.interval,
        }
    }
}

/// Worker процесса: gRPC и фоновый reconciliation под одним сигналом.
pub type DeliveryWorker = ReconcilingWorker<GrpcWorker>;

/// Создаёт [`DeliveryWorker`]: открывает бизнес-порт и reconcilers.
pub struct DeliveryWorkerProvider;

impl<M> Provider<M> for DeliveryWorkerProvider
where
    M: Module + HasComponent<dyn GrpcTransport> + HasComponent<dyn RelationshipReconciliation>,
{
    type Interface = DeliveryWorker;

    fn provide(module: &M) -> Result<Box<DeliveryWorker>, Box<dyn Error>> {
        let transport: &dyn GrpcTransport = module.resolve_ref();
        let reconciliation: &dyn RelationshipReconciliation = module.resolve_ref();
        let worker = transport
            .bind()
            .map_err(|error| format!("listen :{}: {error}", transport.port()))?;
        Ok(Box::new(ReconcilingWorker::new(
            worker,
            reconciliation.reconcilers(),
            reconciliation.schedule(),
        )))
    }
}

module! {
    pub ServerModule {
        components = [TonicTransport, SpiceDbReconciliation],
        providers = [DeliveryWorkerProvider],

        use InfrastructureModule {
            components = [dyn SharedStore],
            providers = []
        }
    }
}

impl ServerModule {
    /// Worker процесса; вызывается после открытия технического порта.
    pub fn worker(&self) -> Result<DeliveryWorker, Box<dyn Error>> {
        HasProvider::<DeliveryWorker>::provide(self).map(|worker| *worker)
    }
}

/// Собирает граф процесса `delivery`. Обязательная инфраструктура
/// (SpiceDB, PostgreSQL) и невалидная конфигурация identity прерывают startup
/// до открытия портов.
pub fn compose(config: &AppConfig) -> Result<ServerModule, Box<dyn Error>> {
    // Невалидный JWKS или allowlist service identity прерывает startup.
    let subjects = subject_resolver(&config.identity)?;
    // SpiceDB обязателен: проверки прав, запись relationships и reconciliation.
    let spicedb = spicedb(&config.spicedb)?;
    let graph: Arc<dyn RelationshipGraph> = Arc::new(spicedb.clone());
    // PostgreSQL обязателен: пустой DATABASE__URL или недоступная база
    // прерывают startup. Revisions записей relationships хранятся с Courier и
    // Delivery; checks читают их из того же хранилища (FR-447).
    let store: Arc<dyn ProcessStore> = Arc::new(postgres(&config.database)?);
    let infrastructure = infrastructure(
        Arc::clone(&store),
        StaticCourierLocator::new(Vec::new()),
        Box::new(spicedb_authorization(spicedb, store)),
        Box::new(SpiceDbRelationships::new(Arc::clone(&graph))),
    );
    let use_cases = Arc::new(service(Arc::clone(&infrastructure)));
    Ok(ServerModule::builder(infrastructure)
        .with_component_parameters::<TonicTransport>(TonicTransportParameters {
            use_cases,
            subjects,
            port: config.server.port,
        })
        .with_component_parameters::<SpiceDbReconciliation>(SpiceDbReconciliationParameters {
            graph,
            grace: config.reconciler.grace,
            page_size: config.reconciler.page_size,
            interval: config.reconciler.interval,
        })
        .build())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn empty_spicedb_endpoint_fails_startup() {
        let Err(error) = spicedb(&SpiceDbConfig::default()) else {
            panic!("an empty endpoint must fail startup");
        };
        assert!(error.to_string().contains(SPICEDB_ENDPOINT_ENV));
    }
}

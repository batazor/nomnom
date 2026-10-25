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
use crate::infrastructure::ids::UuidIdGenerator;
use crate::infrastructure::locator::{CourierPositions, StoredCourierLocator};
use crate::infrastructure::postgres::PostgresStore;
use crate::infrastructure::reconcile::{Reconciler, ReconciliationStore, Schedule};
use crate::infrastructure::spicedb::{
    RelationshipGraph, RepositoryRevisions, RevisionReader, SpiceDbAuthorization, SpiceDbClient,
    SpiceDbConfigError, SpiceDbRelationships,
};
use crate::pkg::config::{AppConfig, DatabaseConfig, SPICEDB_ENDPOINT_ENV, SpiceDbConfig};
use crate::slices::courier::domains::courier::CourierRepository;
use crate::slices::delivery::domains::delivery::DeliveryRepository;
use crate::slices::delivery::infrastructure::DeliveryIntegrationEvents;

use super::ReconcilingWorker;
use super::infrastructure::{ProcessIds, ProcessLocator, ProcessStores, infrastructure};
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
/// Хранилище видно только через роль [`RevisionReader`].
#[must_use]
pub fn spicedb_authorization<S>(client: SpiceDbClient, store: Arc<S>) -> SpiceDbAuthorization
where
    S: ?Sized + CourierRepository + DeliveryRepository + Send + Sync + 'static,
{
    SpiceDbAuthorization::new(Box::new(client))
        .with_revisions(Arc::new(RepositoryRevisions(store)) as Arc<dyn RevisionReader>)
}

/// Идентификаторы и локатор курьеров процесса: UUIDv7, уникальный между
/// перезапусками и репликами (FR-541), и кандидаты из сохранённых позиций
/// доступных курьеров (FR-542).
#[must_use]
pub fn assignment_adapters(
    positions: Arc<dyn CourierPositions>,
) -> (Box<dyn ProcessIds>, Box<dyn ProcessLocator>) {
    (
        Box::new(UuidIdGenerator),
        Box::new(StoredCourierLocator::new(positions)),
    )
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
    /// То же хранилище, что у портов слайсов, в роли reconciliation.
    store: Arc<dyn ReconciliationStore>,
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
                Arc::clone(&self.store),
                self.grace,
                self.page_size,
                Arc::new(Instant::now),
            ),
            Reconciler::deliveries(
                Arc::clone(&self.graph),
                Arc::clone(&self.store),
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
        providers = [DeliveryWorkerProvider]
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
    // Каждая роль хранилища разрешается отдельно над одним `PostgresStore`.
    let store = Arc::new(postgres(&config.database)?);
    let (ids, locator) = assignment_adapters(Arc::clone(&store) as _);
    let infrastructure = infrastructure(
        ProcessStores::over(&store),
        ids,
        locator,
        Box::new(spicedb_authorization(spicedb, Arc::clone(&store))),
        Box::new(SpiceDbRelationships::new(Arc::clone(&graph))),
    );
    let use_cases = Arc::new(service(Arc::clone(&infrastructure)));
    Ok(ServerModule::builder()
        .with_component_parameters::<TonicTransport>(TonicTransportParameters {
            use_cases,
            subjects,
            port: config.server.port,
        })
        .with_component_parameters::<SpiceDbReconciliation>(SpiceDbReconciliationParameters {
            store,
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
    use crate::infrastructure::memory::{MemoryStore, RecordingRelationships};
    use crate::pkg::application::Subject;
    use crate::slices::courier::applications::{
        register_courier, set_courier_availability, update_courier_location,
    };
    use crate::slices::delivery::applications::{create_delivery, find_courier};

    use super::super::infrastructure::local_authorization;
    use super::super::service::ServiceModule;

    fn operator() -> Subject {
        Subject::User("operator-1".to_owned())
    }

    /// Граф use cases с адаптерами процесса над in-memory хранилищем вместо
    /// PostgreSQL; новый граф над тем же хранилищем — перезапуск процесса.
    fn process(store: &Arc<MemoryStore>) -> ServiceModule {
        let (ids, locator) = assignment_adapters(Arc::clone(store) as _);
        service(infrastructure(
            ProcessStores::over(store),
            ids,
            locator,
            Box::new(local_authorization()),
            Box::new(RecordingRelationships::new()),
        ))
    }

    fn create(process: &ServiceModule, order: &str) -> create_delivery::Output {
        process
            .deliveries()
            .create_delivery(
                &operator(),
                create_delivery::Input {
                    order_id: order.to_owned(),
                    pickup_address: "Pickup street 1".to_owned(),
                    dropoff_address: "Dropoff street 2".to_owned(),
                },
            )
            .expect("delivery created")
    }

    /// AC-631
    #[test]
    fn delivery_ids_do_not_repeat_after_restart() {
        let store = Arc::new(MemoryStore::new());
        let before = create(&process(&store), "order-1");

        let after = create(&process(&store), "order-2");

        assert!(after.created);
        assert_ne!(after.delivery_id, before.delivery_id);
    }

    /// AC-632
    #[test]
    fn find_courier_returns_available_courier_with_stored_position() {
        let store = Arc::new(MemoryStore::new());
        let process = process(&store);
        let couriers = process.couriers();
        couriers
            .register_courier(
                &operator(),
                register_courier::Input {
                    courier_id: "courier-1".to_owned(),
                },
            )
            .expect("courier registered");
        couriers
            .set_courier_availability(
                &operator(),
                set_courier_availability::Input {
                    courier_id: "courier-1".to_owned(),
                    availability: set_courier_availability::Availability::Available,
                },
            )
            .expect("courier available");
        couriers
            .update_courier_location(
                &operator(),
                update_courier_location::Input {
                    courier_id: "courier-1".to_owned(),
                    latitude: 52.37,
                    longitude: 4.89,
                },
            )
            .expect("position recorded");
        let created = create(&process, "order-1");

        let found = process
            .deliveries()
            .find_courier(
                &operator(),
                find_courier::Input {
                    delivery_id: created.delivery_id.as_str().to_owned(),
                },
            )
            .expect("search");

        assert!(matches!(
            found.outcome,
            find_courier::Outcome::CandidateFound { courier_id, .. }
                if courier_id.as_str() == "courier-1"
        ));
    }

    #[test]
    fn empty_spicedb_endpoint_fails_startup() {
        let Err(error) = spicedb(&SpiceDbConfig::default()) else {
            panic!("an empty endpoint must fail startup");
        };
        assert!(error.to_string().contains(SPICEDB_ENDPOINT_ENV));
    }
}

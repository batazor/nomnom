//! Composition root процесса `delivery` (`cmd/delivery`): gRPC API, фоновый
//! reconciliation relationships, фоновое истечение Delivery Offers и
//! Dispatch Deliveries.
//!
//! [`compose`] выбирает реализации портов и открывает общие клиенты процесса
//! (fail fast: SpiceDB, PostgreSQL, Valkey потока позиций, service identity), собирает граф use cases
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
use std::sync::{Arc, Mutex};
use std::time::{Duration, Instant};

use shaku::{Component, HasComponent, HasProvider, Interface, Module, Provider, module};

use crate::infrastructure::grpc::{GrpcWorker, SubjectResolver, subject_resolver};
use crate::infrastructure::ids::UuidIdGenerator;
use crate::infrastructure::locator::{CourierPositions, StoredCourierLocator};
use crate::infrastructure::random::SystemRandomSource;
use crate::infrastructure::reconcile::{Reconciler, ReconciliationStore, Schedule};
use crate::infrastructure::repository::{Store, migrate};
use crate::infrastructure::spicedb::{
    CheckMetrics, RelationshipGraph, RepositoryRevisions, RevisionReader, SpiceDbAuthorization,
    SpiceDbClient, SpiceDbConfigError, SpiceDbRelationships,
};
use crate::infrastructure::tracks::ValkeyPositionPublisher;
use crate::pkg::config::{
    AppConfig, OffersConfig, SimulatorConfig, SpiceDbConfig, TrackingConfig, ValkeyConfig, database,
};
use crate::pkg::telemetry::{self, Telemetry};
use crate::slices::courier::domains::courier::CourierRepository;
use crate::slices::delivery::domains::delivery::{DeliveryRepository, VirtualCity};
use crate::slices::delivery::infrastructure::DeliveryIntegrationEvents;
use crate::slices::simulator::di::SimulatorStore;

use super::infrastructure::{
    ProcessIds, ProcessLocator, ProcessStores, ProcessTracking, SystemClock, infrastructure,
};
use super::service::{ServiceModule, service};
use super::{
    DispatchDeliveries, DispatchSchedule, DispatchWorker, ExpireOffers, ExpirySchedule,
    OfferExpiryWorker, ReconcilingWorker, Simulator, SimulatorSchedule, SimulatorWorker,
    SystemRandomness, UseCaseCourierActions,
};

/// `service.name` процесса по умолчанию (`OTEL_SERVICE_NAME` переопределяет).
pub const SERVICE_NAME: &str = "delivery";

/// Первый проход reconciliation вскоре после старта.
const FIRST_RECONCILIATION: Duration = Duration::from_secs(10);

/// Клиент SpiceDB процесса: проверки прав, запись relationships и
/// reconciliation делят одно соединение. Пустой endpoint — ошибка startup.
pub fn spicedb(config: &SpiceDbConfig) -> Result<SpiceDbClient, SpiceDbConfigError> {
    SpiceDbClient::connect(config)
}

/// Хранилище процесса — PostgreSQL. Сначала применяются встроенные миграции
/// (`sqlx::migrate!`, advisory lock сериализует старт реплик): актуальная схема —
/// no-op, иначе ожидающие версии применяются до открытия пула. Пустой
/// `DATABASE__URI`, недоступная база или ошибка миграции — ошибка startup
/// (fail fast).
///
/// Domain Events с контрактом `nomnom/delivery/v1/events.proto` пишутся в
/// Outbox той же транзакцией (ADR 31); публикует их коннектор Debezium (ADR 37).
pub fn store(config: &database::Config) -> Result<Store, String> {
    let applied = migrate(config)?;
    if applied.is_empty() {
        tracing::info!(
            operation = "migrate",
            outcome = "success",
            "schema is up to date"
        );
    } else {
        tracing::info!(operation = "migrate", outcome = "success", applied = ?applied, "migrations applied");
    }
    Store::connect(config)
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
    SpiceDbAuthorization::new(Box::new(client), CheckMetrics::global())
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

/// Virtual City процесса (FR-658): рамка вне диапазонов широты и долготы —
/// ошибка startup.
pub fn virtual_city(config: &TrackingConfig) -> Result<VirtualCity, String> {
    VirtualCity::new(
        config.city_origin_latitude,
        config.city_origin_longitude,
        config.city_blocks,
        config.block_meters,
    )
    .map_err(|error| error.to_string())
}

/// Live tracking процесса: Virtual City, поток позиций в Valkey и
/// случайность ОС. Недоступный Valkey — ошибка startup (FR-666).
pub fn tracking(
    tracking: &TrackingConfig,
    valkey: &ValkeyConfig,
) -> Result<ProcessTracking, String> {
    Ok(ProcessTracking {
        city: virtual_city(tracking)?,
        persist_interval_seconds: tracking.persist_interval_seconds(),
        publisher: Box::new(
            ValkeyPositionPublisher::connect(&valkey.url).map_err(|error| error.to_string())?,
        ),
        random: Box::new(SystemRandomSource),
    })
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

/// Фоновое истечение Delivery Offers (Expire Delivery Offers) над use cases
/// процесса.
pub trait OfferExpiry: Interface {
    fn expire(&self) -> ExpireOffers;
    fn schedule(&self) -> ExpirySchedule;
}

#[derive(Component)]
#[shaku(interface = OfferExpiry)]
pub struct UseCaseOfferExpiry {
    /// Тот же граф use cases, что у gRPC-транспорта.
    use_cases: Arc<ServiceModule>,
    config: OffersConfig,
}

impl OfferExpiry for UseCaseOfferExpiry {
    fn expire(&self) -> ExpireOffers {
        let use_cases = Arc::clone(&self.use_cases);
        Arc::new(move |input| use_cases.deliveries().expire_delivery_offers(input))
    }

    fn schedule(&self) -> ExpirySchedule {
        ExpirySchedule {
            interval: self.config.expiry_interval,
            batch_size: usize::try_from(self.config.expiry_batch).unwrap_or(usize::MAX),
        }
    }
}

/// Фоновый Dispatch Deliveries над use cases процесса: поиск курьера для
/// ожидающих доставок начинается здесь, а не в Create Delivery.
pub trait DeliveryDispatch: Interface {
    fn dispatch(&self) -> DispatchDeliveries;
    fn schedule(&self) -> DispatchSchedule;
}

#[derive(Component)]
#[shaku(interface = DeliveryDispatch)]
pub struct UseCaseDeliveryDispatch {
    /// Тот же граф use cases, что у gRPC-транспорта.
    use_cases: Arc<ServiceModule>,
    config: OffersConfig,
}

impl DeliveryDispatch for UseCaseDeliveryDispatch {
    fn dispatch(&self) -> DispatchDeliveries {
        let use_cases = Arc::clone(&self.use_cases);
        Arc::new(move |input| use_cases.deliveries().dispatch_deliveries(input))
    }

    fn schedule(&self) -> DispatchSchedule {
        DispatchSchedule {
            interval: self.config.dispatch_interval,
            batch_size: usize::try_from(self.config.dispatch_batch).unwrap_or(usize::MAX),
        }
    }
}

/// Демо Simulator курьеров (FR-605, FR-662) над use cases процесса.
pub trait DemoSimulator: Interface {
    fn simulator(&self) -> Simulator;
    fn interval(&self) -> Duration;
    fn track_interval(&self) -> Duration;
}

#[derive(Component)]
#[shaku(interface = DemoSimulator)]
pub struct UseCaseDemoSimulator {
    /// Тот же граф use cases, что у gRPC-транспорта.
    use_cases: Arc<ServiceModule>,
    /// Переключатель и работа Simulator в хранилище процесса.
    store: Arc<dyn SimulatorStore>,
    config: SimulatorConfig,
    city: VirtualCity,
}

impl DemoSimulator for UseCaseDemoSimulator {
    fn simulator(&self) -> Simulator {
        Simulator::new(
            Arc::clone(&self.store),
            Arc::new(UseCaseCourierActions::new(Arc::clone(&self.use_cases))),
            Arc::new(SystemClock),
            Box::new(SystemRandomness::seeded()),
            SimulatorSchedule {
                interval: self.config.interval,
                min_delay: self.config.min_delay,
                max_delay: self.config.max_delay,
            },
            self.city,
        )
    }

    fn interval(&self) -> Duration {
        self.config.interval
    }

    fn track_interval(&self) -> Duration {
        self.config.track_interval
    }
}

/// Worker процесса: gRPC, фоновый reconciliation, истечение offers,
/// Dispatch Deliveries и Simulator под одним сигналом.
pub type DeliveryWorker =
    ReconcilingWorker<SimulatorWorker<DispatchWorker<OfferExpiryWorker<GrpcWorker>>>>;

/// Создаёт [`DeliveryWorker`]: открывает бизнес-порт, reconcilers, цикл
/// истечения offers и цикл Dispatch Deliveries.
pub struct DeliveryWorkerProvider;

impl<M> Provider<M> for DeliveryWorkerProvider
where
    M: Module
        + HasComponent<dyn GrpcTransport>
        + HasComponent<dyn RelationshipReconciliation>
        + HasComponent<dyn OfferExpiry>
        + HasComponent<dyn DeliveryDispatch>
        + HasComponent<dyn DemoSimulator>,
{
    type Interface = DeliveryWorker;

    fn provide(module: &M) -> Result<Box<DeliveryWorker>, Box<dyn Error>> {
        let transport: &dyn GrpcTransport = module.resolve_ref();
        let reconciliation: &dyn RelationshipReconciliation = module.resolve_ref();
        let expiry: &dyn OfferExpiry = module.resolve_ref();
        let dispatch: &dyn DeliveryDispatch = module.resolve_ref();
        let simulator: &dyn DemoSimulator = module.resolve_ref();
        let worker = transport
            .bind()
            .map_err(|error| format!("listen :{}: {error}", transport.port()))?;
        let worker = OfferExpiryWorker::new(worker, expiry.expire(), expiry.schedule());
        let worker = DispatchWorker::new(worker, dispatch.dispatch(), dispatch.schedule());
        let worker = SimulatorWorker::new(
            worker,
            simulator.simulator(),
            simulator.interval(),
            simulator.track_interval(),
        );
        Ok(Box::new(ReconcilingWorker::new(
            worker,
            reconciliation.reconcilers(),
            reconciliation.schedule(),
        )))
    }
}

/// Metrics pipeline процесса (`pkg/telemetry`): бизнес-метрики use cases.
pub trait ServerTelemetry: Interface {
    /// Выгружает накопленные точки; повторный вызов ничего не делает.
    fn shutdown(&self);
}

#[derive(Component)]
#[shaku(interface = ServerTelemetry)]
pub struct OtlpServerTelemetry {
    /// `None` после shutdown.
    telemetry: Mutex<Option<Telemetry>>,
}

impl ServerTelemetry for OtlpServerTelemetry {
    fn shutdown(&self) {
        if let Some(telemetry) = self
            .telemetry
            .lock()
            .ok()
            .and_then(|mut telemetry| telemetry.take())
        {
            telemetry.shutdown();
        }
    }
}

module! {
    pub ServerModule {
        components = [
            TonicTransport,
            SpiceDbReconciliation,
            UseCaseOfferExpiry,
            UseCaseDeliveryDispatch,
            UseCaseDemoSimulator,
            OtlpServerTelemetry
        ],
        providers = [DeliveryWorkerProvider]
    }
}

impl ServerModule {
    /// Worker процесса; вызывается после открытия технического порта.
    pub fn worker(&self) -> Result<DeliveryWorker, Box<dyn Error>> {
        HasProvider::<DeliveryWorker>::provide(self).map(|worker| *worker)
    }

    /// Выгружает метрики после остановки worker.
    pub fn shutdown(&self) {
        HasComponent::<dyn ServerTelemetry>::resolve_ref(self).shutdown();
    }
}

/// Собирает граф процесса `delivery`. Обязательная инфраструктура
/// (SpiceDB, PostgreSQL) и невалидная конфигурация identity прерывают startup
/// до открытия портов.
/// Вызывается внутри tokio runtime: OTLP exporter держит соединение на нём.
pub fn compose(config: &AppConfig) -> Result<ServerModule, Box<dyn Error>> {
    // Tracer и instruments (клиент SpiceDB, пул PostgreSQL, use cases)
    // берутся у глобальных providers при создании, поэтому pipeline
    // запускается первым.
    let telemetry = telemetry::start(SERVICE_NAME)?;
    // Невалидный JWKS или allowlist service identity прерывает startup.
    let subjects = subject_resolver(&config.identity)?;
    // SpiceDB обязателен: проверки прав, запись relationships и reconciliation.
    let spicedb = spicedb(&config.spicedb)?;
    let graph: Arc<dyn RelationshipGraph> = Arc::new(spicedb.clone());
    // PostgreSQL обязателен: пустой DATABASE__URI, недоступная база или
    // ошибка миграции схемы прерывают startup. Revisions записей relationships хранятся с Courier и
    // Delivery; checks читают их из того же хранилища (FR-447).
    // Каждая роль хранилища разрешается отдельно над одним `Store`.
    let store = Arc::new(store(&config.database)?);
    let (ids, locator) = assignment_adapters(Arc::clone(&store) as _);
    // Valkey потока позиций обязателен; Virtual City вне диапазонов —
    // ошибка startup.
    let tracking = tracking(&config.tracking, &config.valkey)?;
    let city = tracking.city;
    let infrastructure = infrastructure(
        ProcessStores::over(&store),
        ids,
        locator,
        Box::new(spicedb_authorization(spicedb, Arc::clone(&store))),
        Box::new(SpiceDbRelationships::new(Arc::clone(&graph))),
        tracking,
    );
    let use_cases = Arc::new(service(Arc::clone(&infrastructure)));
    Ok(ServerModule::builder()
        .with_component_parameters::<UseCaseOfferExpiry>(UseCaseOfferExpiryParameters {
            use_cases: Arc::clone(&use_cases),
            config: config.offers,
        })
        .with_component_parameters::<UseCaseDeliveryDispatch>(UseCaseDeliveryDispatchParameters {
            use_cases: Arc::clone(&use_cases),
            config: config.offers,
        })
        .with_component_parameters::<UseCaseDemoSimulator>(UseCaseDemoSimulatorParameters {
            use_cases: Arc::clone(&use_cases),
            store: Arc::clone(&store) as _,
            config: config.simulator,
            city,
        })
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
        .with_component_parameters::<OtlpServerTelemetry>(OtlpServerTelemetryParameters {
            telemetry: Mutex::new(Some(telemetry)),
        })
        .build())
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::infrastructure::memory::RecordingRelationships;
    use crate::infrastructure::repository::test_support::TestStore;
    use crate::pkg::application::Subject;
    use crate::pkg::config::SPICEDB_ENDPOINT_ENV;
    use crate::slices::courier::applications::{
        register_courier, set_courier_availability, update_courier_location,
    };
    use crate::slices::delivery::applications::{
        create_delivery, dispatch_deliveries, find_courier,
    };

    use super::super::infrastructure::local_authorization;
    use super::super::service::ServiceModule;

    fn operator() -> Subject {
        Subject::User("operator-1".to_owned())
    }

    fn tracking_config(city_blocks: u32, block_meters: f64, latitude: f64) -> TrackingConfig {
        TrackingConfig {
            city_origin_latitude: latitude,
            city_origin_longitude: 13.385,
            city_blocks,
            block_meters,
            persist_interval: Duration::from_secs(15),
        }
    }

    /// AC-871, FR-665: the grid rules live only in VirtualCity; an invalid
    /// grid stops the process before ports open.
    #[test]
    fn invalid_virtual_city_fails_startup() {
        assert!(virtual_city(&tracking_config(12, 150.0, 52.515)).is_ok());
        for (name, config) in [
            ("one block", tracking_config(1, 150.0, 52.515)),
            ("zero block", tracking_config(12, 0.0, 52.515)),
            ("beyond the pole", tracking_config(12, 150.0, 89.999)),
        ] {
            assert!(virtual_city(&config).is_err(), "{name}");
        }
    }

    /// Граф use cases с адаптерами процесса над тестовым PostgreSQL; новый
    /// граф над тем же хранилищем — перезапуск процесса.
    fn process(store: &Arc<TestStore>) -> ServiceModule {
        let (ids, locator) = assignment_adapters(Arc::clone(store) as _);
        service(infrastructure(
            ProcessStores::over(store),
            ids,
            locator,
            Box::new(local_authorization()),
            Box::new(RecordingRelationships::new()),
            crate::di::service::test_tracking(),
        ))
    }

    fn create(process: &ServiceModule, order: &str) -> create_delivery::Output {
        process
            .deliveries()
            .create_delivery(
                &operator(),
                create_delivery::Input {
                    order_id: order.to_owned(),
                    market_id: "berlin".to_owned(),
                },
            )
            .expect("delivery created")
    }

    /// AC-631
    #[test]
    fn delivery_ids_do_not_repeat_after_restart() {
        let store = Arc::new(TestStore::new());
        let before = create(&process(&store), "order-1");

        let after = create(&process(&store), "order-2");

        assert!(after.created);
        assert_ne!(after.delivery_id, before.delivery_id);
    }

    /// AC-632
    #[test]
    fn find_courier_returns_available_courier_with_stored_position() {
        let store = Arc::new(TestStore::new());
        let process = process(&store);
        let couriers = process.couriers();
        couriers
            .register_courier(
                &operator(),
                register_courier::Input {
                    courier_id: "courier-1".to_owned(),
                    name: "Timur".to_owned(),
                    market_id: "berlin".to_owned(),
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

    /// AC-734: the courier search of a created Delivery starts on the next
    /// dispatch pass of the process, over the stored courier positions.
    #[test]
    fn dispatch_offers_a_created_delivery_to_a_courier_on_line() {
        let store = Arc::new(TestStore::new());
        let process = process(&store);
        let couriers = process.couriers();
        couriers
            .register_courier(
                &operator(),
                register_courier::Input {
                    courier_id: "courier-1".to_owned(),
                    name: "Timur".to_owned(),
                    market_id: "berlin".to_owned(),
                },
            )
            .expect("courier registered");
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

        let output = process
            .deliveries()
            .dispatch_deliveries(dispatch_deliveries::Input {
                batch_size: 10,
                after: None,
            })
            .expect("dispatched");

        assert_eq!(output.offered.len(), 1);
        assert_eq!(output.offered[0].delivery_id, created.delivery_id);
        assert_eq!(output.offered[0].courier_id.as_str(), "courier-1");
    }

    #[test]
    fn empty_spicedb_endpoint_fails_startup() {
        let Err(error) = spicedb(&SpiceDbConfig::default()) else {
            panic!("an empty endpoint must fail startup");
        };
        assert!(error.to_string().contains(SPICEDB_ENDPOINT_ENV));
    }
}

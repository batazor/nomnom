//! Composition root процесса `delivery` (`cmd/delivery`): gRPC API, фоновый
//! reconciliation relationships и consumer собственных фактов Delivery для
//! Delivery Duration Statistics (FR-676). Поиск курьера ведёт Assignment
//! Workflow процесса `delivery-worker` ([`super::worker`]); use cases этого
//! процесса запускают и будят его через Temporal (FR-707).
//!
//! [`compose`] выбирает реализации портов и открывает общие клиенты процесса
//! (fail fast: SpiceDB, PostgreSQL, Valkey потока позиций, Kafka consumer,
//! Temporal, service identity, политика Delivery Duration Estimate), собирает граф use cases
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
use crate::infrastructure::messaging::{EventConsumer, MessageHandler};
use crate::infrastructure::random::SystemRandomSource;
use crate::infrastructure::reconcile::{Reconciler, ReconciliationStore, Schedule};
use crate::infrastructure::repository::{OUTBOX_TOPIC, Store, migrate};
use crate::infrastructure::spicedb::{
    CheckMetrics, RelationshipGraph, RepositoryRevisions, RevisionReader, SpiceDbAuthorization,
    SpiceDbClient, SpiceDbConfigError, SpiceDbRelationships,
};
use crate::infrastructure::temporal::TemporalAssignment;
use crate::infrastructure::tracks::ValkeyPositionPublisher;
use crate::pkg::config::{
    AppConfig, DELIVERY_ESTIMATE_BASELINE_COURIER_LEAD_ENV, DELIVERY_ESTIMATE_BASELINE_TRANSIT_ENV,
    DELIVERY_ESTIMATE_SAMPLE_SIZE_ENV, DeliveryEstimateConfig, KafkaConfig, SimulatorConfig,
    SpiceDbConfig, TemporalConfig, TrackingConfig, ValkeyConfig, database,
};
use crate::pkg::telemetry::{self, Telemetry};
use crate::slices::courier::domains::courier::CourierRepository;
use crate::slices::delivery::applications::record_delivery_duration::CONSUMER;
use crate::slices::delivery::domains::delivery::{
    DeliveryEstimatePolicy, DeliveryRepository, DurationKind, InvalidEstimatePolicy, VirtualCity,
};
use crate::slices::delivery::infrastructure::{
    DeliveryIntegrationEvents, duration_input, handling_error,
};
use crate::slices::simulator::di::SimulatorStore;

use super::infrastructure::{
    AssignmentAdapters, ProcessAssignment, ProcessStores, ProcessTracking, SystemClock,
    infrastructure,
};
use super::service::{ServiceModule, service};
use super::{
    ConsumingWorker, ReconcilingWorker, Simulator, SimulatorSchedule, SimulatorWorker,
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

/// Адаптеры назначения процесса: UUIDv7, уникальный между перезапусками и
/// репликами (FR-541), кандидаты из сохранённых позиций доступных курьеров
/// (FR-542) и выбранный Assignment Workflow.
#[must_use]
pub fn assignment_adapters(
    positions: Arc<dyn CourierPositions>,
    process: Arc<dyn ProcessAssignment>,
) -> AssignmentAdapters {
    AssignmentAdapters {
        ids: Box::new(UuidIdGenerator),
        locator: Box::new(StoredCourierLocator::new(positions)),
        process,
    }
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

/// Политика Delivery Duration Estimate процесса (FR-691): нулевой размер
/// выборки или нулевая базовая длительность — ошибка startup.
pub fn estimate_policy(config: &DeliveryEstimateConfig) -> Result<DeliveryEstimatePolicy, String> {
    DeliveryEstimatePolicy::new(
        config.sample_size,
        config.baseline_courier_lead,
        config.baseline_transit,
    )
    .map_err(|error| {
        let name = match error {
            InvalidEstimatePolicy::SampleSize => DELIVERY_ESTIMATE_SAMPLE_SIZE_ENV,
            InvalidEstimatePolicy::Baseline(DurationKind::CourierLead) => {
                DELIVERY_ESTIMATE_BASELINE_COURIER_LEAD_ENV
            }
            InvalidEstimatePolicy::Baseline(DurationKind::Transit) => {
                DELIVERY_ESTIMATE_BASELINE_TRANSIT_ENV
            }
        };
        format!("{name}: {error}")
    })
}

/// Consumer собственных фактов Delivery (`delivery.v1.delivery-events`) для
/// Delivery Duration Statistics в группе `KAFKA__CONSUMER_GROUP`.
pub fn duration_statistics_consumer(config: &KafkaConfig) -> Result<EventConsumer, String> {
    EventConsumer::subscribe(config, OUTBOX_TOPIC, CONSUMER)
}

/// Temporal-клиент Assignment Workflow (FR-707): недоступный frontend —
/// ошибка startup. Courier Candidate будит доставку из выборки `store`.
pub fn assignment(
    config: &TemporalConfig,
    store: Arc<Store>,
) -> Result<TemporalAssignment, String> {
    TemporalAssignment::connect(config, store)
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

/// Consumer Delivery Duration Statistics (FR-676) над use cases процесса.
pub trait DurationStatistics: Interface {
    /// Consumer и обработчик его сообщений; consumer отдаётся один раз —
    /// единственному worker процесса.
    fn consumer(&self) -> Result<(EventConsumer, MessageHandler), String>;
}

#[derive(Component)]
#[shaku(interface = DurationStatistics)]
pub struct KafkaDurationStatistics {
    /// Тот же граф use cases, что у gRPC-транспорта.
    use_cases: Arc<ServiceModule>,
    /// `None` после передачи worker.
    consumer: Mutex<Option<EventConsumer>>,
}

impl DurationStatistics for KafkaDurationStatistics {
    fn consumer(&self) -> Result<(EventConsumer, MessageHandler), String> {
        let consumer = self
            .consumer
            .lock()
            .map_err(|_| "duration statistics consumer lock poisoned".to_owned())?
            .take()
            .ok_or_else(|| "duration statistics consumer is already running".to_owned())?;
        let use_cases = Arc::clone(&self.use_cases);
        let handler: MessageHandler = Arc::new(move |envelope, payload| {
            let Some(input) = duration_input(envelope, payload)? else {
                return Ok(());
            };
            use_cases
                .deliveries()
                .record_delivery_duration(input)
                .map(drop)
                .map_err(|error| handling_error(&error))
        });
        Ok((consumer, handler))
    }
}

/// Worker процесса: gRPC, фоновый reconciliation, Simulator и consumer
/// Delivery Duration Statistics под одним сигналом.
pub type DeliveryWorker = ConsumingWorker<ReconcilingWorker<SimulatorWorker<GrpcWorker>>>;

/// Создаёт [`DeliveryWorker`]: открывает бизнес-порт, reconcilers и
/// Simulator.
pub struct DeliveryWorkerProvider;

impl<M> Provider<M> for DeliveryWorkerProvider
where
    M: Module
        + HasComponent<dyn GrpcTransport>
        + HasComponent<dyn RelationshipReconciliation>
        + HasComponent<dyn DemoSimulator>
        + HasComponent<dyn DurationStatistics>,
{
    type Interface = DeliveryWorker;

    fn provide(module: &M) -> Result<Box<DeliveryWorker>, Box<dyn Error>> {
        let transport: &dyn GrpcTransport = module.resolve_ref();
        let reconciliation: &dyn RelationshipReconciliation = module.resolve_ref();
        let simulator: &dyn DemoSimulator = module.resolve_ref();
        let statistics: &dyn DurationStatistics = module.resolve_ref();
        let (consumer, handler) = statistics.consumer()?;
        let worker = transport
            .bind()
            .map_err(|error| format!("listen :{}: {error}", transport.port()))?;
        let worker = SimulatorWorker::new(
            worker,
            simulator.simulator(),
            simulator.interval(),
            simulator.track_interval(),
        );
        let worker = ReconcilingWorker::new(
            worker,
            reconciliation.reconcilers(),
            reconciliation.schedule(),
        );
        Ok(Box::new(ConsumingWorker::new(worker, consumer, handler)))
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
            UseCaseDemoSimulator,
            KafkaDurationStatistics,
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
    // Valkey потока позиций обязателен; Virtual City вне диапазонов —
    // ошибка startup.
    let tracking = tracking(&config.tracking, &config.valkey)?;
    let city = tracking.city;
    // Нулевая выборка или базовая длительность Delivery Duration Estimate —
    // ошибка startup; невалидная конфигурация Kafka consumer — тоже.
    let estimate = estimate_policy(&config.delivery_estimate)?;
    let consumer = duration_statistics_consumer(&config.kafka)?;
    // Temporal обязателен: Create Delivery и ответы курьеров запускают и
    // будят Assignment Workflow.
    let assignment = Arc::new(assignment(&config.temporal, Arc::clone(&store))?);
    let infrastructure = infrastructure(
        ProcessStores::over(&store),
        assignment_adapters(Arc::clone(&store) as _, assignment),
        Box::new(spicedb_authorization(spicedb, Arc::clone(&store))),
        Box::new(SpiceDbRelationships::new(Arc::clone(&graph))),
        tracking,
        estimate,
    );
    let use_cases = Arc::new(service(Arc::clone(&infrastructure)));
    Ok(ServerModule::builder()
        .with_component_parameters::<UseCaseDemoSimulator>(UseCaseDemoSimulatorParameters {
            use_cases: Arc::clone(&use_cases),
            store: Arc::clone(&store) as _,
            config: config.simulator,
            city,
        })
        .with_component_parameters::<KafkaDurationStatistics>(KafkaDurationStatisticsParameters {
            use_cases: Arc::clone(&use_cases),
            consumer: Mutex::new(Some(consumer)),
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
    use crate::infrastructure::memory::{RecordingAssignment, RecordingRelationships};
    use crate::infrastructure::repository::test_support::TestStore;
    use crate::pkg::application::Subject;
    use crate::pkg::config::SPICEDB_ENDPOINT_ENV;
    use crate::slices::courier::applications::{
        register_courier, set_courier_availability, update_courier_location,
    };
    use crate::slices::delivery::applications::{
        advance_assignment, create_delivery, find_courier,
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
        service(infrastructure(
            ProcessStores::over(store),
            assignment_adapters(Arc::clone(store) as _, Arc::new(RecordingAssignment::new())),
            Box::new(local_authorization()),
            Box::new(RecordingRelationships::new()),
            crate::di::service::test_tracking(),
            crate::di::service::test_estimate_policy(),
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

    /// AC-959: the first step of the Assignment Workflow offers a created
    /// Delivery to a courier on line, over the stored courier positions.
    #[test]
    fn assignment_offers_a_created_delivery_to_a_courier_on_line() {
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
            .advance_assignment(advance_assignment::Input {
                delivery_id: created.delivery_id.as_str().to_owned(),
            })
            .expect("advanced");

        let advance_assignment::Output::Offered { offer_id, .. } = output else {
            panic!("the courier on line is offered the delivery, got {output:?}");
        };
        let offer = crate::slices::delivery::domains::delivery::DeliveryOfferRepository::get(
            &*store, &offer_id,
        )
        .expect("read offer")
        .expect("offer stored");
        assert_eq!(offer.delivery_id(), &created.delivery_id);
        assert_eq!(offer.courier_id().as_str(), "courier-1");
    }

    /// FR-691: a zero `DELIVERY_ESTIMATE__*` stops the process before ports
    /// open; the error names the variable.
    #[test]
    fn zero_delivery_estimate_fails_startup() {
        let minutes = |value: u64| Duration::from_secs(value * 60);
        let config = |sample_size, courier_lead, transit| DeliveryEstimateConfig {
            sample_size,
            baseline_courier_lead: courier_lead,
            baseline_transit: transit,
        };
        assert!(estimate_policy(&config(50, minutes(15), minutes(20))).is_ok());
        for (name, config) in [
            (
                DELIVERY_ESTIMATE_SAMPLE_SIZE_ENV,
                config(0, minutes(15), minutes(20)),
            ),
            (
                DELIVERY_ESTIMATE_BASELINE_COURIER_LEAD_ENV,
                config(50, Duration::ZERO, minutes(20)),
            ),
            (
                DELIVERY_ESTIMATE_BASELINE_TRANSIT_ENV,
                config(50, minutes(15), Duration::ZERO),
            ),
        ] {
            let Err(error) = estimate_policy(&config) else {
                panic!("{name} = 0 must fail startup");
            };
            assert!(error.contains(name), "{error}");
        }
    }

    #[test]
    fn empty_spicedb_endpoint_fails_startup() {
        let Err(error) = spicedb(&SpiceDbConfig::default()) else {
            panic!("an empty endpoint must fail startup");
        };
        assert!(error.to_string().contains(SPICEDB_ENDPOINT_ENV));
    }
}

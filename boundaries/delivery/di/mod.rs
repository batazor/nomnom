//! Composition root процесса `delivery`: выбирает реализации портов и
//! собирает shaku-модули слайсов в итоговый [`ServiceModule`].
//!
//! Авторизация — SpiceDB (`infrastructure::spicedb`), если задан
//! `SPICEDB__ENDPOINT`. Пока хранилище и locator не выбраны, остальные порты
//! закрываются in-memory адаптерами из `infrastructure::memory`.

mod http_worker;
mod reconciling_worker;

pub use http_worker::HttpWorker;
pub use reconciling_worker::ReconcilingWorker;

use std::sync::Arc;
use std::time::{SystemTime, UNIX_EPOCH};

use shaku::{Component, Interface, module};

use crate::infrastructure::memory::{
    MemoryStore, RecordingRelationships, SequentialIdGenerator, StaticAuthorization,
    StaticCourierLocator,
};
use crate::infrastructure::spicedb::{
    RepositoryRevisions, RevisionReader, SpiceDbAuthorization, SpiceDbClient, SpiceDbConfigError,
};
use crate::pkg::application::{Clock, IdGenerator, TransactionScope};
use crate::pkg::config::{SPICEDB_ENDPOINT_ENV, SpiceDbConfig};
use crate::slices::courier::applications::authorization::CourierAuthorization;
use crate::slices::courier::applications::get_courier_current_delivery::CourierCurrentDeliveryReadModel;
use crate::slices::courier::applications::relationships::CourierRelationshipWriter;
use crate::slices::courier::applications::update_courier_location::CourierLocationStore;
use crate::slices::courier::domains::courier::CourierRepository;
use crate::slices::delivery::applications::authorization::DeliveryAuthorization;
use crate::slices::delivery::applications::courier_locator::CourierLocator;
use crate::slices::delivery::applications::list_available_delivery_offers::PendingDeliveryOffersReadModel;
use crate::slices::delivery::applications::outbox::DeliveryOutbox;
use crate::slices::delivery::applications::policy::AssignmentPolicy;
use crate::slices::delivery::applications::relationships::DeliveryRelationshipWriter;
use crate::slices::delivery::domains::delivery::{DeliveryOfferRepository, DeliveryRepository};

use crate::slices::courier::di::{
    CourierDependencies, CourierModule, CourierPorts, CourierUseCases,
};
use crate::slices::delivery::di::{
    DeliveryDependencies, DeliveryModule, DeliveryPorts, DeliverySettings, DeliveryUseCases,
};

/// Локальные значения политики назначения до появления продуктовых настроек.
const LOCAL_ASSIGNMENT_POLICY: AssignmentPolicy = AssignmentPolicy {
    maximum_distance_meters: 5_000,
    maximum_active_assignments: 1,
    offer_ttl_seconds: 60,
};

/// Общее хранилище: одно на процесс, чтобы Unit of Work, репозитории и
/// Outbox обоих слайсов работали с одними данными.
pub trait SharedStore: Interface {
    fn store(&self) -> &MemoryStore;
}

#[derive(Component)]
#[shaku(interface = SharedStore)]
pub struct LocalStore {
    /// Shared with the reconciler, which reads business state of the process.
    #[shaku(default)]
    store: Arc<MemoryStore>,
}

impl SharedStore for LocalStore {
    fn store(&self) -> &MemoryStore {
        &self.store
    }
}

/// Адаптер авторизации процесса: закрывает порты обоих слайсов.
pub trait ProcessAuthorization: CourierAuthorization + DeliveryAuthorization + Send + Sync {}

impl<T: CourierAuthorization + DeliveryAuthorization + Send + Sync> ProcessAuthorization for T {}

/// Выбирает авторизацию процесса: SpiceDB при заданном endpoint, иначе
/// локальный адаптер с предупреждением.
pub fn authorization(
    config: &SpiceDbConfig,
) -> Result<Box<dyn ProcessAuthorization>, SpiceDbConfigError> {
    match SpiceDbAuthorization::from_config(config)? {
        Some(spicedb) => Ok(Box::new(spicedb)),
        None => Err(SpiceDbConfigError::missing_endpoint(SPICEDB_ENDPOINT_ENV)),
    }
}

/// Клиент SpiceDB процесса: проверки прав, запись relationships и
/// reconciliation делят одно соединение. Пустой endpoint — ошибка startup.
pub fn spicedb(config: &SpiceDbConfig) -> Result<SpiceDbClient, SpiceDbConfigError> {
    if !config.enabled() {
        return Err(SpiceDbConfigError::missing_endpoint(SPICEDB_ENDPOINT_ENV));
    }
    SpiceDbClient::connect(config)
}

/// Разрешающая авторизация тестового графа `build()`; процесс без SpiceDB не
/// стартует (см. [`authorization`]).
fn local_authorization() -> StaticAuthorization {
    StaticAuthorization::allow_all()
}

/// Одна проверка прав на оба слайса.
pub trait SharedAuthorization: Interface {
    fn courier(&self) -> &dyn CourierAuthorization;
    fn delivery(&self) -> &dyn DeliveryAuthorization;
}

#[derive(Component)]
#[shaku(interface = SharedAuthorization)]
pub struct LocalAuthorization {
    #[shaku(default = Box::new(local_authorization()))]
    authorization: Box<dyn ProcessAuthorization>,
}

impl SharedAuthorization for LocalAuthorization {
    fn courier(&self) -> &dyn CourierAuthorization {
        &*self.authorization
    }

    fn delivery(&self) -> &dyn DeliveryAuthorization {
        &*self.authorization
    }
}

/// Relationship writers of the process: one adapter closes both slices.
pub trait ProcessRelationships:
    CourierRelationshipWriter + DeliveryRelationshipWriter + Send + Sync
{
}

impl<T: CourierRelationshipWriter + DeliveryRelationshipWriter + Send + Sync> ProcessRelationships
    for T
{
}

/// Relationship writers of both slices.
pub trait SharedRelationships: Interface {
    fn courier(&self) -> &dyn CourierRelationshipWriter;
    fn delivery(&self) -> &dyn DeliveryRelationshipWriter;
}

#[derive(Component)]
#[shaku(interface = SharedRelationships)]
pub struct LocalRelationships {
    /// Writes nowhere; the process passes the SpiceDB adapter.
    #[shaku(default = Box::new(RecordingRelationships::new()))]
    relationships: Box<dyn ProcessRelationships>,
}

impl SharedRelationships for LocalRelationships {
    fn courier(&self) -> &dyn CourierRelationshipWriter {
        &*self.relationships
    }

    fn delivery(&self) -> &dyn DeliveryRelationshipWriter {
        &*self.relationships
    }
}

pub trait ProcessClock: Clock + Interface {}

impl<T: Clock + Interface> ProcessClock for T {}

#[derive(Component)]
#[shaku(interface = ProcessClock)]
pub struct SystemClock;

impl Clock for SystemClock {
    fn now_epoch_seconds(&self) -> i64 {
        SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .map_or(0, |elapsed| {
                i64::try_from(elapsed.as_secs()).unwrap_or(i64::MAX)
            })
    }
}

#[derive(Component)]
#[shaku(interface = CourierPorts)]
pub struct LocalCourierPorts {
    #[shaku(inject)]
    store: Arc<dyn SharedStore>,
    #[shaku(inject)]
    authorization: Arc<dyn SharedAuthorization>,
    #[shaku(inject)]
    clock: Arc<dyn ProcessClock>,
    #[shaku(inject)]
    relationships: Arc<dyn SharedRelationships>,
}

impl CourierPorts for LocalCourierPorts {
    fn authorization(&self) -> &dyn CourierAuthorization {
        self.authorization.courier()
    }

    fn couriers(&self) -> &dyn CourierRepository {
        self.store.store()
    }

    fn locations(&self) -> &dyn CourierLocationStore {
        self.store.store()
    }

    fn current_deliveries(&self) -> &dyn CourierCurrentDeliveryReadModel {
        self.store.store()
    }

    fn clock(&self) -> &dyn Clock {
        &*self.clock
    }

    fn relationships(&self) -> &dyn CourierRelationshipWriter {
        self.relationships.courier()
    }
}

#[derive(Component)]
#[shaku(interface = DeliveryPorts)]
pub struct LocalDeliveryPorts {
    #[shaku(inject)]
    store: Arc<dyn SharedStore>,
    #[shaku(inject)]
    authorization: Arc<dyn SharedAuthorization>,
    #[shaku(inject)]
    clock: Arc<dyn ProcessClock>,
    #[shaku(inject)]
    relationships: Arc<dyn SharedRelationships>,
    #[shaku(default = StaticCourierLocator::new(Vec::new()))]
    locator: StaticCourierLocator,
    #[shaku(default = SequentialIdGenerator::new("delivery"))]
    ids: SequentialIdGenerator,
}

impl DeliveryPorts for LocalDeliveryPorts {
    fn authorization(&self) -> &dyn DeliveryAuthorization {
        self.authorization.delivery()
    }

    fn transactions(&self) -> &dyn TransactionScope {
        self.store.store()
    }

    fn deliveries(&self) -> &dyn DeliveryRepository {
        self.store.store()
    }

    fn offers(&self) -> &dyn DeliveryOfferRepository {
        self.store.store()
    }

    fn pending_offers(&self) -> &dyn PendingDeliveryOffersReadModel {
        self.store.store()
    }

    fn couriers(&self) -> &dyn CourierRepository {
        self.store.store()
    }

    fn outbox(&self) -> &dyn DeliveryOutbox {
        self.store.store()
    }

    fn locator(&self) -> &dyn CourierLocator {
        &self.locator
    }

    fn clock(&self) -> &dyn Clock {
        &*self.clock
    }

    fn ids(&self) -> &dyn IdGenerator {
        &self.ids
    }

    fn relationships(&self) -> &dyn DeliveryRelationshipWriter {
        self.relationships.delivery()
    }
}

#[derive(Component)]
#[shaku(interface = DeliverySettings)]
pub struct LocalDeliverySettings;

impl DeliverySettings for LocalDeliverySettings {
    fn assignment_policy(&self) -> AssignmentPolicy {
        LOCAL_ASSIGNMENT_POLICY
    }
}

module! {
    pub InfrastructureModule: CourierDependencies {
        components = [
            LocalStore,
            LocalAuthorization,
            LocalRelationships,
            SystemClock,
            LocalCourierPorts,
            LocalDeliveryPorts,
            LocalDeliverySettings
        ],
        providers = []
    }
}

impl DeliveryDependencies for InfrastructureModule {}

module! {
    pub ServiceModule {
        components = [],
        providers = [],

        use CourierModule {
            components = [dyn CourierUseCases],
            providers = []
        },

        use DeliveryModule {
            components = [dyn DeliveryUseCases],
            providers = []
        }
    }
}

/// Собирает граф процесса с локальной авторизацией.
#[must_use]
pub fn build() -> ServiceModule {
    build_with_authorization(Box::new(local_authorization()))
}

/// Собирает граф процесса: инфраструктура → модули слайсов → итоговый модуль.
#[must_use]
pub fn build_with_authorization(authorization: Box<dyn ProcessAuthorization>) -> ServiceModule {
    assemble(
        Arc::new(MemoryStore::new()),
        StaticCourierLocator::new(Vec::new()),
        authorization,
        Box::new(RecordingRelationships::new()),
    )
    .module
}

/// Граф процесса и его хранилище: хранилище читает reconciler.
pub struct Process {
    pub module: ServiceModule,
    pub store: Arc<MemoryStore>,
}

/// Собирает граф процесса над `store` с выбранными адаптерами авторизации и
/// записи relationships. Хранилище создаётся снаружи: его же читают
/// reconciler и [`RevisionReader`] авторизации.
#[must_use]
pub fn build_process(
    store: Arc<MemoryStore>,
    authorization: Box<dyn ProcessAuthorization>,
    relationships: Box<dyn ProcessRelationships>,
) -> Process {
    assemble(
        store,
        StaticCourierLocator::new(Vec::new()),
        authorization,
        relationships,
    )
}

/// SpiceDB-авторизация процесса: checks `courier:<id>` и `delivery:<id>`
/// видят собственные записи relationships по revision из `store` (FR-447).
#[must_use]
pub fn spicedb_authorization(
    client: SpiceDbClient,
    store: Arc<MemoryStore>,
) -> SpiceDbAuthorization {
    SpiceDbAuthorization::new(Box::new(client))
        .with_revisions(Arc::new(RepositoryRevisions(store)) as Arc<dyn RevisionReader>)
}

/// Граф процесса с заданными кандидатами locator — для транспортных тестов.
#[cfg(test)]
pub(crate) fn build_with_locator(locator: StaticCourierLocator) -> ServiceModule {
    assemble(
        Arc::new(MemoryStore::new()),
        locator,
        Box::new(local_authorization()),
        Box::new(RecordingRelationships::new()),
    )
    .module
}

fn assemble(
    store: Arc<MemoryStore>,
    locator: StaticCourierLocator,
    authorization: Box<dyn ProcessAuthorization>,
    relationships: Box<dyn ProcessRelationships>,
) -> Process {
    let infrastructure = Arc::new(
        InfrastructureModule::builder()
            .with_component_parameters::<LocalStore>(LocalStoreParameters {
                store: Arc::clone(&store),
            })
            .with_component_parameters::<LocalAuthorization>(LocalAuthorizationParameters {
                authorization,
            })
            .with_component_parameters::<LocalRelationships>(LocalRelationshipsParameters {
                relationships,
            })
            .with_component_parameters::<LocalDeliveryPorts>(LocalDeliveryPortsParameters {
                locator,
                ids: SequentialIdGenerator::new("delivery"),
            })
            .build(),
    );
    let courier = Arc::new(CourierModule::builder(infrastructure.clone()).build());
    let delivery = Arc::new(DeliveryModule::builder(infrastructure).build());
    Process {
        module: ServiceModule::builder(courier, delivery).build(),
        store,
    }
}

#[cfg(test)]
mod tests {
    use shaku::HasComponent;

    use crate::pkg::application::Subject;
    use crate::slices::courier::applications::{get_courier_current_delivery, register_courier};
    use crate::slices::delivery::applications::create_delivery;

    use super::*;

    fn operator() -> Subject {
        Subject::User("operator-1".to_owned())
    }

    #[test]
    fn slices_share_one_store() {
        let module = build();
        let couriers: &dyn CourierUseCases = module.resolve_ref();
        let deliveries: &dyn DeliveryUseCases = module.resolve_ref();

        couriers
            .register_courier(
                &operator(),
                register_courier::Input {
                    courier_id: "courier-1".to_owned(),
                },
            )
            .expect("courier registered");
        let current = couriers
            .get_courier_current_delivery(
                &operator(),
                get_courier_current_delivery::Input {
                    courier_id: "courier-1".to_owned(),
                },
            )
            .expect("courier visible to the courier slice");
        assert_eq!(current.current_delivery, None);

        let created = deliveries
            .create_delivery(
                &operator(),
                create_delivery::Input {
                    order_id: "order-1".to_owned(),
                    pickup_address: "Pickup street 1".to_owned(),
                    dropoff_address: "Dropoff street 2".to_owned(),
                },
            )
            .expect("delivery created");
        assert!(created.created);
        assert_eq!(created.delivery_id.as_str(), "delivery-1");
    }

    #[test]
    fn selected_authorization_closes_both_slices() {
        let module = build_with_authorization(Box::new(StaticAuthorization::unavailable()));
        let couriers: &dyn CourierUseCases = module.resolve_ref();
        let deliveries: &dyn DeliveryUseCases = module.resolve_ref();

        assert!(
            couriers
                .register_courier(
                    &operator(),
                    register_courier::Input {
                        courier_id: "courier-1".to_owned(),
                    },
                )
                .is_err()
        );
        assert!(
            deliveries
                .create_delivery(
                    &operator(),
                    create_delivery::Input {
                        order_id: "order-1".to_owned(),
                        pickup_address: "Pickup street 1".to_owned(),
                        dropoff_address: "Dropoff street 2".to_owned(),
                    },
                )
                .is_err()
        );
    }

    #[test]
    fn empty_spicedb_endpoint_fails_startup() {
        let Err(error) = authorization(&SpiceDbConfig::default()) else {
            panic!("an empty endpoint must fail startup");
        };
        assert!(error.to_string().contains(SPICEDB_ENDPOINT_ENV));
    }
}

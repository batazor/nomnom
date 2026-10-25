//! Порты слайсов и их реализации: [`InfrastructureModule`] закрывает
//! `CourierDependencies` и `DeliveryDependencies` одним хранилищем, одной
//! авторизацией и одним writer'ом relationships на процесс.
//!
//! Реализации выбирает composition root процесса ([`super::server`]) или
//! тестовый граф ([`super::build`]) и передаёт их параметрами компонентов.

use std::sync::Arc;
use std::time::{SystemTime, UNIX_EPOCH};

use shaku::{Component, Interface, module};

use crate::infrastructure::memory::{
    MemoryStore, RecordingRelationships, SequentialIdGenerator, StaticAuthorization,
    StaticCourierLocator,
};
use crate::infrastructure::reconcile::ReconciliationStore;
use crate::pkg::application::{Clock, IdGenerator, TransactionScope};
use crate::slices::courier::applications::assignment::CourierAssignment;
use crate::slices::courier::applications::authorization::CourierAuthorization;
use crate::slices::courier::applications::relationships::CourierRelationshipWriter;
use crate::slices::courier::applications::update_courier_location::CourierLocationStore;
use crate::slices::courier::di::{CourierDependencies, CourierPorts};
use crate::slices::courier::domains::courier::CourierRepository;
use crate::slices::delivery::applications::authorization::DeliveryAuthorization;
use crate::slices::delivery::applications::courier_locator::CourierLocator;
use crate::slices::delivery::applications::get_courier_current_delivery::CourierCurrentDeliveryReadModel;
use crate::slices::delivery::applications::list_available_delivery_offers::PendingDeliveryOffersReadModel;
use crate::slices::delivery::applications::outbox::DeliveryOutbox;
use crate::slices::delivery::applications::policy::AssignmentPolicy;
use crate::slices::delivery::applications::relationships::DeliveryRelationshipWriter;
use crate::slices::delivery::di::{DeliveryDependencies, DeliveryPorts, DeliverySettings};
use crate::slices::delivery::domains::delivery::{DeliveryOfferRepository, DeliveryRepository};

/// Локальные значения политики назначения до появления продуктовых настроек.
const LOCAL_ASSIGNMENT_POLICY: AssignmentPolicy = AssignmentPolicy {
    maximum_distance_meters: 5_000,
    maximum_active_assignments: 1,
    offer_ttl_seconds: 60,
};

/// Все порты хранилища процесса: репозитории, Outbox, read models, Unit of
/// Work и чтение reconciliation. Закрывается одним адаптером
/// ([`PostgresStore`](crate::infrastructure::postgres::PostgresStore) в
/// процессе, [`MemoryStore`] в тестах), поэтому Unit of Work охватывает
/// записи обоих слайсов и Outbox.
pub trait ProcessStore:
    CourierRepository
    + CourierAssignment
    + CourierLocationStore
    + CourierCurrentDeliveryReadModel
    + DeliveryRepository
    + DeliveryOfferRepository
    + PendingDeliveryOffersReadModel
    + DeliveryOutbox
    + TransactionScope
    + ReconciliationStore
    + Send
    + Sync
{
}

impl<T> ProcessStore for T where
    T: CourierRepository
        + CourierAssignment
        + CourierLocationStore
        + CourierCurrentDeliveryReadModel
        + DeliveryRepository
        + DeliveryOfferRepository
        + PendingDeliveryOffersReadModel
        + DeliveryOutbox
        + TransactionScope
        + ReconciliationStore
        + Send
        + Sync
{
}

/// Общее хранилище: одно на процесс, чтобы Unit of Work, репозитории и
/// Outbox обоих слайсов работали с одними данными.
pub trait SharedStore: Interface {
    fn store(&self) -> &dyn ProcessStore;

    /// Владение тем же хранилищем для компонентов процесса, которые живут
    /// дольше одного вызова (reconciler, revisions авторизации).
    fn shared(&self) -> Arc<dyn ProcessStore>;
}

#[derive(Component)]
#[shaku(interface = SharedStore)]
pub struct LocalStore {
    /// Shared with the reconciler, which reads business state of the process.
    #[shaku(default = Arc::new(MemoryStore::new()))]
    store: Arc<dyn ProcessStore>,
}

impl SharedStore for LocalStore {
    fn store(&self) -> &dyn ProcessStore {
        &*self.store
    }

    fn shared(&self) -> Arc<dyn ProcessStore> {
        Arc::clone(&self.store)
    }
}

/// Адаптер авторизации процесса: закрывает порты обоих слайсов.
pub trait ProcessAuthorization: CourierAuthorization + DeliveryAuthorization + Send + Sync {}

impl<T: CourierAuthorization + DeliveryAuthorization + Send + Sync> ProcessAuthorization for T {}

/// Разрешающая авторизация тестового графа [`super::build`]; процесс без
/// SpiceDB не стартует (см. [`super::server::spicedb`]).
pub(super) fn local_authorization() -> StaticAuthorization {
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

    fn courier_authorization(&self) -> &dyn CourierAuthorization {
        self.authorization.courier()
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

    fn current_deliveries(&self) -> &dyn CourierCurrentDeliveryReadModel {
        self.store.store()
    }

    fn courier_assignment(&self) -> &dyn CourierAssignment {
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

/// Собирает порты процесса над выбранными реализациями. Хранилище
/// принадлежит модулю на всё время процесса; транзакции открывает Unit of
/// Work на время одного use case.
#[must_use]
pub(super) fn infrastructure(
    store: Arc<dyn ProcessStore>,
    locator: StaticCourierLocator,
    authorization: Box<dyn ProcessAuthorization>,
    relationships: Box<dyn ProcessRelationships>,
) -> Arc<InfrastructureModule> {
    Arc::new(
        InfrastructureModule::builder()
            .with_component_parameters::<LocalStore>(LocalStoreParameters { store })
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
    )
}

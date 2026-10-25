//! Порты слайсов и их реализации: [`InfrastructureModule`] закрывает
//! `CourierDependencies` и `DeliveryDependencies` ролями одного хранилища
//! ([`ProcessStores`]), одной авторизацией и одним writer'ом relationships на
//! процесс.
//!
//! Реализации выбирает composition root процесса ([`super::server`]) или
//! тестовые графы [`super::service`] и передаёт их параметрами компонентов.

use std::sync::Arc;
use std::time::{SystemTime, UNIX_EPOCH};

use shaku::{Component, Interface, module};

use crate::infrastructure::memory::{RecordingRelationships, StaticAuthorization};
use crate::pkg::application::{Clock, IdGenerator, TransactionScope, UnitOfWorkError};
use crate::pkg::persistence::RepositoryError;
use crate::slices::courier::applications::assignment::{CourierAssignment, CourierAssignmentError};
use crate::slices::courier::applications::authorization::CourierAuthorization;
use crate::slices::courier::applications::relationships::CourierRelationshipWriter;
use crate::slices::courier::applications::update_courier_location::CourierLocationStore;
use crate::slices::courier::di::{CourierDependencies, CourierPorts};
use crate::slices::courier::domains::courier::{CourierId, CourierRepository};
use crate::slices::delivery::applications::authorization::DeliveryAuthorization;
use crate::slices::delivery::applications::courier_locator::CourierLocator;
use crate::slices::delivery::applications::dispatch_deliveries::WaitingDeliveries;
use crate::slices::delivery::applications::expire_delivery_offers::ExpiredDeliveryOffers;
use crate::slices::delivery::applications::offers_of_delivery::OffersOfDelivery;
use crate::slices::delivery::applications::outbox::{DeliveryOutbox, OutboxMessage};
use crate::slices::delivery::applications::policy::AssignmentPolicy;
use crate::slices::delivery::applications::relationships::DeliveryRelationshipWriter;
use crate::slices::delivery::di::{
    DeliveryDependencies, DeliveryPorts, DeliveryReadModels, DeliverySettings, TransactionalStore,
    WaitingDeliveriesQuery,
};
use crate::slices::delivery::domains::delivery::{
    Delivery, DeliveryFailureCode, DeliveryId, DeliveryOffer, DeliveryOfferId,
    DeliveryOfferRepository, DeliveryRepository, OrderId,
};
use crate::slices::delivery::infrastructure::failure_log::FailureLog;
use crate::slices::delivery::infrastructure::observability::{DomainMetrics, ObservedOutbox};

/// Локальные значения политики назначения до появления продуктовых настроек.
const LOCAL_ASSIGNMENT_POLICY: AssignmentPolicy = AssignmentPolicy {
    maximum_distance_meters: 5_000,
    maximum_active_assignments: 1,
    offer_ttl_seconds: 60,
};

/// Роль хранилища, которую видит слайс `courier`: `Courier` и последние
/// позиции курьеров.
pub trait CourierStore: CourierRepository + CourierLocationStore + Send + Sync {}

impl<T: CourierRepository + CourierLocationStore + Send + Sync> CourierStore for T {}

/// Роли хранилища процесса над одним адаптером
/// ([`PostgresStore`](crate::infrastructure::postgres::PostgresStore); в
/// тестах — он же за тестовыми хуками `TestStore`), поэтому Unit of Work охватывает записи обоих слайсов и Outbox.
/// Каждый потребитель получает только свою роль; reconciliation и revisions
/// авторизации composition root разрешает над тем же `Arc` отдельно.
pub struct ProcessStores {
    pub couriers: Arc<dyn CourierStore>,
    pub transactional: Arc<dyn TransactionalStore>,
    pub read_models: Arc<dyn DeliveryReadModels>,
    /// Выборка Dispatch Deliveries: только PostgreSQL.
    pub waiting: Arc<dyn WaitingDeliveriesQuery>,
}

impl ProcessStores {
    /// Все роли слайсов над одним хранилищем (PostgreSQL).
    #[must_use]
    pub fn over<S>(store: &Arc<S>) -> Self
    where
        S: CourierStore
            + TransactionalStore
            + DeliveryReadModels
            + WaitingDeliveriesQuery
            + 'static,
    {
        Self {
            couriers: Arc::clone(store) as _,
            transactional: Arc::clone(store) as _,
            read_models: Arc::clone(store) as _,
            waiting: Arc::clone(store) as _,
        }
    }
}

/// Адаптер авторизации процесса: закрывает порты обоих слайсов.
pub trait ProcessAuthorization: CourierAuthorization + DeliveryAuthorization + Send + Sync {}

impl<T: CourierAuthorization + DeliveryAuthorization + Send + Sync> ProcessAuthorization for T {}

/// Разрешающая авторизация тестовых графов [`super::service`]; процесс без
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

/// Генератор идентификаторов `Delivery` и offer: UUIDv7 в процессе
/// ([`UuidIdGenerator`](crate::infrastructure::ids::UuidIdGenerator)),
/// последовательный только в тестах (FR-541).
pub trait ProcessIds: IdGenerator + Send + Sync {}

impl<T: IdGenerator + Send + Sync> ProcessIds for T {}

/// Локатор курьеров: сохранённые позиции в процессе
/// ([`StoredCourierLocator`](crate::infrastructure::locator::StoredCourierLocator)),
/// статический только в тестах (FR-542).
pub trait ProcessLocator: CourierLocator + Send + Sync {}

impl<T: CourierLocator + Send + Sync> ProcessLocator for T {}

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
    store: Arc<dyn CourierStore>,
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
        &*self.store
    }

    fn locations(&self) -> &dyn CourierLocationStore {
        &*self.store
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
    store: ObservedStore,
    read_models: Arc<dyn DeliveryReadModels>,
    waiting: Arc<dyn WaitingDeliveriesQuery>,
    #[shaku(inject)]
    authorization: Arc<dyn SharedAuthorization>,
    #[shaku(inject)]
    clock: Arc<dyn ProcessClock>,
    #[shaku(inject)]
    relationships: Arc<dyn SharedRelationships>,
    locator: Box<dyn ProcessLocator>,
    ids: Box<dyn ProcessIds>,
}

/// Closed set of `nomnom.reason` of `nomnom.delivery.failed`: the codes of the
/// Delivery Failure Reason catalog (the free-text detail is never recorded).
const DELIVERY_FAILURE_REASONS: &[&str] = &DeliveryFailureCode::CODES;

/// Business metrics and the Delivery Failure log of the process.
fn observed_outbox() -> ObservedOutbox {
    ObservedOutbox::new(DomainMetrics::global(DELIVERY_FAILURE_REASONS))
        .with_failure_log(FailureLog)
}

/// [`TransactionalStore`] of the slice whose Unit of Work and Outbox go
/// through [`ObservedOutbox`]: business metrics are recorded after the
/// outermost commit. Repositories and courier assignment delegate as is.
pub struct ObservedStore {
    inner: Arc<dyn TransactionalStore>,
    observed: ObservedOutbox,
}

impl ObservedStore {
    #[must_use]
    pub fn new(inner: Arc<dyn TransactionalStore>, observed: ObservedOutbox) -> Self {
        Self { inner, observed }
    }
}

impl TransactionScope for ObservedStore {
    fn run_scoped(&self, work: &mut dyn FnMut() -> bool) -> Result<(), UnitOfWorkError> {
        self.observed.run_scoped(&*self.inner, work)
    }
}

impl DeliveryOutbox for ObservedStore {
    fn add(&self, message: OutboxMessage) -> Result<(), RepositoryError> {
        self.observed.add(&*self.inner, message)
    }
}

impl DeliveryRepository for ObservedStore {
    fn get(&self, id: &DeliveryId) -> Result<Option<Delivery>, RepositoryError> {
        DeliveryRepository::get(&*self.inner, id)
    }

    fn find_by_order(&self, order_id: &OrderId) -> Result<Option<Delivery>, RepositoryError> {
        self.inner.find_by_order(order_id)
    }

    fn add(&self, delivery: &Delivery) -> Result<(), RepositoryError> {
        DeliveryRepository::add(&*self.inner, delivery)
    }

    fn save(&self, delivery: &Delivery) -> Result<(), RepositoryError> {
        DeliveryRepository::save(&*self.inner, delivery)
    }
}

impl DeliveryOfferRepository for ObservedStore {
    fn get(&self, id: &DeliveryOfferId) -> Result<Option<DeliveryOffer>, RepositoryError> {
        DeliveryOfferRepository::get(&*self.inner, id)
    }

    fn add(&self, offer: &DeliveryOffer) -> Result<(), RepositoryError> {
        DeliveryOfferRepository::add(&*self.inner, offer)
    }

    fn save(&self, offer: &DeliveryOffer) -> Result<(), RepositoryError> {
        DeliveryOfferRepository::save(&*self.inner, offer)
    }
}

impl OffersOfDelivery for ObservedStore {
    fn offer_ids_of(
        &self,
        delivery_id: &DeliveryId,
    ) -> Result<Vec<DeliveryOfferId>, RepositoryError> {
        self.inner.offer_ids_of(delivery_id)
    }
}

impl ExpiredDeliveryOffers for ObservedStore {
    fn expired_pending_offer_ids(
        &self,
        now_epoch_seconds: i64,
        limit: usize,
    ) -> Result<Vec<DeliveryOfferId>, RepositoryError> {
        self.inner
            .expired_pending_offer_ids(now_epoch_seconds, limit)
    }
}

impl CourierAssignment for ObservedStore {
    fn ensure_registered(&self, courier_id: &CourierId) -> Result<(), CourierAssignmentError> {
        self.inner.ensure_registered(courier_id)
    }

    fn ensure_available(&self, courier_id: &CourierId) -> Result<(), CourierAssignmentError> {
        self.inner.ensure_available(courier_id)
    }

    fn occupy(&self, courier_id: &CourierId) -> Result<(), CourierAssignmentError> {
        self.inner.occupy(courier_id)
    }

    fn release(&self, courier_id: &CourierId) -> Result<(), CourierAssignmentError> {
        self.inner.release(courier_id)
    }
}

impl DeliveryPorts for LocalDeliveryPorts {
    fn authorization(&self) -> &dyn DeliveryAuthorization {
        self.authorization.delivery()
    }

    fn courier_authorization(&self) -> &dyn CourierAuthorization {
        self.authorization.courier()
    }

    fn store(&self) -> &dyn TransactionalStore {
        &self.store
    }

    fn read_models(&self) -> &dyn DeliveryReadModels {
        &*self.read_models
    }

    fn waiting_deliveries(&self) -> &dyn WaitingDeliveries {
        &*self.waiting
    }

    fn locator(&self) -> &dyn CourierLocator {
        &*self.locator
    }

    fn clock(&self) -> &dyn Clock {
        &*self.clock
    }

    fn ids(&self) -> &dyn IdGenerator {
        &*self.ids
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

/// Собирает порты процесса над выбранными реализациями. Роли хранилища
/// принадлежат модулю на всё время процесса; транзакции открывает Unit of
/// Work на время одного use case.
#[must_use]
pub(super) fn infrastructure(
    stores: ProcessStores,
    ids: Box<dyn ProcessIds>,
    locator: Box<dyn ProcessLocator>,
    authorization: Box<dyn ProcessAuthorization>,
    relationships: Box<dyn ProcessRelationships>,
) -> Arc<InfrastructureModule> {
    Arc::new(
        InfrastructureModule::builder()
            .with_component_parameters::<LocalAuthorization>(LocalAuthorizationParameters {
                authorization,
            })
            .with_component_parameters::<LocalRelationships>(LocalRelationshipsParameters {
                relationships,
            })
            .with_component_parameters::<LocalCourierPorts>(LocalCourierPortsParameters {
                store: stores.couriers,
            })
            .with_component_parameters::<LocalDeliveryPorts>(LocalDeliveryPortsParameters {
                store: ObservedStore::new(stores.transactional, observed_outbox()),
                read_models: stores.read_models,
                waiting: stores.waiting,
                locator,
                ids,
            })
            .build(),
    )
}

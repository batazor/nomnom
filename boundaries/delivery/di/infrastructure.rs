//! Порты слайсов и их реализации: [`InfrastructureModule`] закрывает
//! `CourierDependencies` и `DeliveryDependencies` ролями одного хранилища
//! ([`ProcessStores`]), одной авторизацией и одним writer'ом relationships на
//! процесс.
//!
//! Реализации выбирает composition root процесса ([`super::server`]) или
//! тестовые графы [`super::service`] и передаёт их параметрами компонентов.

use std::sync::Arc;
use std::time::{Duration, SystemTime, UNIX_EPOCH};

use shaku::{Component, Interface, module};

#[cfg(test)]
use crate::infrastructure::memory::StaticAuthorization;
use crate::pkg::persistence::RepositoryError;
use crate::pkg::unit_of_work::{TransactionScope, UnitOfWorkError};
use crate::pkg::usecase::{Clock, IdGenerator};
use crate::slices::courier::applications::assignment::{CourierAssignment, CourierAssignmentError};
use crate::slices::courier::applications::authorization::CourierAuthorization;
use crate::slices::courier::applications::candidate_wake::CandidateWake;
use crate::slices::courier::applications::relationships::CourierRelationshipWriter;
use crate::slices::courier::applications::update_courier_location::{
    ActiveTrackingChannel, CourierLocationStore, CourierPositionPublisher,
};
use crate::slices::courier::di::{CourierDependencies, CourierPorts, CourierSettings};
use crate::slices::courier::domains::courier::{
    CourierId, CourierRepository, CourierStatus, MarketId,
};
use crate::slices::delivery::applications::assignment_process::AssignmentProcess;
use crate::slices::delivery::applications::authorization::DeliveryAuthorization;
use crate::slices::delivery::applications::courier_locator::CourierLocator;
use crate::slices::delivery::applications::offers_of_delivery::OffersOfDelivery;
use crate::slices::delivery::applications::outbox::{DeliveryOutbox, OutboxMessage};
use crate::slices::delivery::applications::policy::AssignmentPolicy;
use crate::slices::delivery::applications::record_delivery_duration::{
    DeliveryDurationStatistics, DeliveryInbox, OpenTransit,
};
use crate::slices::delivery::applications::relationships::DeliveryRelationshipWriter;
use crate::slices::delivery::applications::tracking::RandomSource;
use crate::slices::delivery::di::{
    DeliveryDependencies, DeliveryPorts, DeliveryReadModels, DeliverySettings, TransactionalStore,
};
use crate::slices::delivery::domains::delivery::{
    Delivery, DeliveryEstimatePolicy, DeliveryFailureCode, DeliveryId, DeliveryOffer,
    DeliveryOfferId, DeliveryOfferRepository, DeliveryRepository, DurationKind, OrderId,
    VirtualCity,
};
use crate::slices::delivery::infrastructure::failure_log::FailureLog;
use crate::slices::delivery::infrastructure::observability::{DomainMetrics, ObservedOutbox};
use crate::slices::delivery::infrastructure::transitions::TransitionMetrics;
use crate::slices::simulator::applications::authorization::SimulatorAuthorization;
use crate::slices::simulator::di::{SimulatorDependencies, SimulatorPorts, SimulatorStore};

/// Локальные значения политики назначения до появления продуктовых настроек.
pub(super) const LOCAL_ASSIGNMENT_POLICY: AssignmentPolicy = AssignmentPolicy {
    maximum_distance_meters: 5_000,
    maximum_active_assignments: 1,
    offer_ttl_seconds: 60,
};

/// Роль хранилища, которую видит слайс `courier`: `Courier`, последние
/// позиции курьеров и Tracking Channel активной доставки курьера.
pub trait CourierStore:
    CourierRepository + CourierLocationStore + ActiveTrackingChannel + Send + Sync
{
}

impl<T> CourierStore for T where
    T: CourierRepository + CourierLocationStore + ActiveTrackingChannel + Send + Sync
{
}

/// Поток позиций курьеров: Valkey pub/sub в процессе
/// ([`ValkeyPositionPublisher`](crate::infrastructure::tracks::ValkeyPositionPublisher)),
/// записывающий только в тестах.
pub trait ProcessPositionPublisher: CourierPositionPublisher + Send + Sync {}

impl<T: CourierPositionPublisher + Send + Sync> ProcessPositionPublisher for T {}

/// Случайность точек и каналов новых доставок: ОС в процессе
/// ([`SystemRandomSource`](crate::infrastructure::random::SystemRandomSource)),
/// фиксированная только в тестах.
pub trait ProcessRandomSource: RandomSource + Send + Sync {}

impl<T: RandomSource + Send + Sync> ProcessRandomSource for T {}

/// Live tracking процесса (FR-658…FR-661): Virtual City, интервал записи
/// позиции, поток позиций и случайность новых доставок.
pub struct ProcessTracking {
    pub city: VirtualCity,
    pub persist_interval_seconds: i64,
    pub publisher: Box<dyn ProcessPositionPublisher>,
    pub random: Box<dyn ProcessRandomSource>,
}

/// Роли хранилища процесса над одним адаптером
/// ([`Store`](crate::infrastructure::repository::Store); в
/// тестах — он же за тестовыми хуками `TestStore`), поэтому Unit of Work охватывает записи обоих слайсов и Outbox.
/// Каждый потребитель получает только свою роль; reconciliation и revisions
/// авторизации composition root разрешает над тем же `Arc` отдельно.
pub struct ProcessStores {
    pub couriers: Arc<dyn CourierStore>,
    pub transactional: Arc<dyn TransactionalStore>,
    pub read_models: Arc<dyn DeliveryReadModels>,
    /// Переключатель и работа Simulator.
    pub simulator: Arc<dyn SimulatorStore>,
}

impl ProcessStores {
    /// Все роли слайсов над одним хранилищем (PostgreSQL).
    #[must_use]
    pub fn over<S>(store: &Arc<S>) -> Self
    where
        S: CourierStore + TransactionalStore + DeliveryReadModels + SimulatorStore + 'static,
    {
        Self {
            couriers: Arc::clone(store) as _,
            transactional: Arc::clone(store) as _,
            read_models: Arc::clone(store) as _,
            simulator: Arc::clone(store) as _,
        }
    }
}

/// Assignment Workflow процесса: Temporal
/// ([`TemporalAssignment`](crate::infrastructure::temporal::TemporalAssignment)),
/// записывающий только в тестах. Один адаптер закрывает start и wake слайса
/// `delivery` и пробуждение поиска курьером обоих слайсов.
pub trait ProcessAssignment: AssignmentProcess + CandidateWake + Send + Sync {}

impl<T: AssignmentProcess + CandidateWake + Send + Sync> ProcessAssignment for T {}

/// Адаптеры назначения процесса: идентификаторы, локатор курьеров и
/// Assignment Workflow.
pub struct AssignmentAdapters {
    pub ids: Box<dyn ProcessIds>,
    pub locator: Box<dyn ProcessLocator>,
    pub process: Arc<dyn ProcessAssignment>,
}

/// Адаптер авторизации процесса: закрывает порты всех слайсов.
pub trait ProcessAuthorization:
    CourierAuthorization + DeliveryAuthorization + SimulatorAuthorization + Send + Sync
{
}

impl<T> ProcessAuthorization for T where
    T: CourierAuthorization + DeliveryAuthorization + SimulatorAuthorization + Send + Sync
{
}

/// Разрешающая авторизация тестовых графов [`super::service`]; процесс без
/// SpiceDB не стартует (см. [`super::server::spicedb`]).
#[cfg(test)]
pub(super) fn local_authorization() -> StaticAuthorization {
    StaticAuthorization::allow_all()
}

/// Одна проверка прав на все слайсы.
pub trait SharedAuthorization: Interface {
    fn courier(&self) -> &dyn CourierAuthorization;
    fn delivery(&self) -> &dyn DeliveryAuthorization;
    fn simulator(&self) -> &dyn SimulatorAuthorization;
}

#[derive(Component)]
#[shaku(interface = SharedAuthorization)]
pub struct LocalAuthorization {
    authorization: Box<dyn ProcessAuthorization>,
}

impl SharedAuthorization for LocalAuthorization {
    fn courier(&self) -> &dyn CourierAuthorization {
        &*self.authorization
    }

    fn delivery(&self) -> &dyn DeliveryAuthorization {
        &*self.authorization
    }

    fn simulator(&self) -> &dyn SimulatorAuthorization {
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
    publisher: Box<dyn ProcessPositionPublisher>,
    assignment: Arc<dyn ProcessAssignment>,
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

    fn channels(&self) -> &dyn ActiveTrackingChannel {
        &*self.store
    }

    fn publisher(&self) -> &dyn CourierPositionPublisher {
        &*self.publisher
    }

    fn clock(&self) -> &dyn Clock {
        &*self.clock
    }

    fn relationships(&self) -> &dyn CourierRelationshipWriter {
        self.relationships.courier()
    }

    fn wake(&self) -> &dyn CandidateWake {
        &*self.assignment
    }
}

#[derive(Component)]
#[shaku(interface = DeliveryPorts)]
pub struct LocalDeliveryPorts {
    store: ObservedStore,
    read_models: Arc<dyn DeliveryReadModels>,
    assignment: Arc<dyn ProcessAssignment>,
    #[shaku(inject)]
    authorization: Arc<dyn SharedAuthorization>,
    #[shaku(inject)]
    clock: Arc<dyn ProcessClock>,
    #[shaku(inject)]
    relationships: Arc<dyn SharedRelationships>,
    locator: Box<dyn ProcessLocator>,
    ids: Box<dyn ProcessIds>,
    city: VirtualCity,
    random: Box<dyn ProcessRandomSource>,
}

/// Closed set of `nomnom.reason` of `nomnom.delivery.failed`: the codes of the
/// Delivery Failure Reason catalog (the free-text detail is never recorded).
const DELIVERY_FAILURE_REASONS: &[&str] = &DeliveryFailureCode::CODES;

/// Business metrics, FSM transitions and the Delivery Failure log of the
/// process.
pub(super) fn observed_outbox() -> ObservedOutbox {
    ObservedOutbox::new(
        DomainMetrics::global(DELIVERY_FAILURE_REASONS),
        TransitionMetrics::global(),
    )
    .with_failure_log(FailureLog)
}

/// [`TransactionalStore`] of the slice whose Unit of Work, Outbox and
/// aggregate repositories go through [`ObservedOutbox`]: business metrics and
/// FSM transitions are recorded after the outermost commit. Queries and
/// courier assignment delegate as is.
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
        self.observed.get_delivery(&*self.inner, id)
    }

    fn find_by_order(&self, order_id: &OrderId) -> Result<Option<Delivery>, RepositoryError> {
        self.observed.find_delivery_by_order(&*self.inner, order_id)
    }

    fn add(&self, delivery: &Delivery) -> Result<(), RepositoryError> {
        self.observed.add_delivery(&*self.inner, delivery)
    }

    fn save(&self, delivery: &Delivery) -> Result<(), RepositoryError> {
        self.observed.save_delivery(&*self.inner, delivery)
    }
}

impl DeliveryOfferRepository for ObservedStore {
    fn get(&self, id: &DeliveryOfferId) -> Result<Option<DeliveryOffer>, RepositoryError> {
        self.observed.get_offer(&*self.inner, id)
    }

    fn add(&self, offer: &DeliveryOffer) -> Result<(), RepositoryError> {
        self.observed.add_offer(&*self.inner, offer)
    }

    fn save(&self, offer: &DeliveryOffer) -> Result<(), RepositoryError> {
        self.observed.save_offer(&*self.inner, offer)
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

impl DeliveryInbox for ObservedStore {
    fn claim(
        &self,
        consumer: &str,
        event_id: &str,
        event_type: &str,
    ) -> Result<bool, RepositoryError> {
        self.inner.claim(consumer, event_id, event_type)
    }
}

impl DeliveryDurationStatistics for ObservedStore {
    fn open_transit(
        &self,
        delivery_id: &DeliveryId,
        market_id: &MarketId,
        picked_up_at_ms: i64,
    ) -> Result<(), RepositoryError> {
        self.inner
            .open_transit(delivery_id, market_id, picked_up_at_ms)
    }

    fn close_transit(
        &self,
        delivery_id: &DeliveryId,
    ) -> Result<Option<OpenTransit>, RepositoryError> {
        self.inner.close_transit(delivery_id)
    }

    fn record_duration(
        &self,
        market_id: &MarketId,
        kind: DurationKind,
        delivery_id: &DeliveryId,
        duration: Duration,
        keep: u32,
    ) -> Result<(), RepositoryError> {
        self.inner
            .record_duration(market_id, kind, delivery_id, duration, keep)
    }
}

impl CourierAssignment for ObservedStore {
    fn status(&self, courier_id: &CourierId) -> Result<CourierStatus, CourierAssignmentError> {
        self.inner.status(courier_id)
    }

    fn ensure_available(
        &self,
        courier_id: &CourierId,
        market_id: &MarketId,
    ) -> Result<(), CourierAssignmentError> {
        self.inner.ensure_available(courier_id, market_id)
    }

    fn occupy(
        &self,
        courier_id: &CourierId,
        market_id: &MarketId,
    ) -> Result<(), CourierAssignmentError> {
        self.inner.occupy(courier_id, market_id)
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

    fn city(&self) -> &VirtualCity {
        &self.city
    }

    fn random(&self) -> &dyn RandomSource {
        &*self.random
    }

    fn assignment(&self) -> &dyn AssignmentProcess {
        &*self.assignment
    }

    fn candidates(&self) -> &dyn CandidateWake {
        &*self.assignment
    }
}

#[derive(Component)]
#[shaku(interface = SimulatorPorts)]
pub struct LocalSimulatorPorts {
    store: Arc<dyn SimulatorStore>,
    #[shaku(inject)]
    authorization: Arc<dyn SharedAuthorization>,
}

impl SimulatorPorts for LocalSimulatorPorts {
    fn authorization(&self) -> &dyn SimulatorAuthorization {
        self.authorization.simulator()
    }

    fn store(&self) -> &dyn SimulatorStore {
        &*self.store
    }
}

#[derive(Component)]
#[shaku(interface = CourierSettings)]
pub struct LocalCourierSettings {
    persist_interval_seconds: i64,
}

impl CourierSettings for LocalCourierSettings {
    fn persist_interval_seconds(&self) -> i64 {
        self.persist_interval_seconds
    }
}

#[derive(Component)]
#[shaku(interface = DeliverySettings)]
pub struct LocalDeliverySettings {
    /// `DELIVERY_ESTIMATE__*` of the process (FR-691).
    estimate: DeliveryEstimatePolicy,
}

impl DeliverySettings for LocalDeliverySettings {
    fn assignment_policy(&self) -> AssignmentPolicy {
        LOCAL_ASSIGNMENT_POLICY
    }

    fn estimate_policy(&self) -> DeliveryEstimatePolicy {
        self.estimate
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
            LocalSimulatorPorts,
            LocalCourierSettings,
            LocalDeliverySettings
        ],
        providers = []
    }
}

impl DeliveryDependencies for InfrastructureModule {}

impl SimulatorDependencies for InfrastructureModule {}

/// Собирает порты процесса над выбранными реализациями. Роли хранилища
/// принадлежат модулю на всё время процесса; транзакции открывает Unit of
/// Work на время одного use case.
#[must_use]
pub(super) fn infrastructure(
    stores: ProcessStores,
    assignment: AssignmentAdapters,
    authorization: Box<dyn ProcessAuthorization>,
    relationships: Box<dyn ProcessRelationships>,
    tracking: ProcessTracking,
    estimate: DeliveryEstimatePolicy,
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
                publisher: tracking.publisher,
                assignment: Arc::clone(&assignment.process),
            })
            .with_component_parameters::<LocalCourierSettings>(LocalCourierSettingsParameters {
                persist_interval_seconds: tracking.persist_interval_seconds,
            })
            .with_component_parameters::<LocalDeliverySettings>(LocalDeliverySettingsParameters {
                estimate,
            })
            .with_component_parameters::<LocalSimulatorPorts>(LocalSimulatorPortsParameters {
                store: stores.simulator,
            })
            .with_component_parameters::<LocalDeliveryPorts>(LocalDeliveryPortsParameters {
                store: ObservedStore::new(stores.transactional, observed_outbox()),
                read_models: stores.read_models,
                assignment: assignment.process,
                locator: assignment.locator,
                ids: assignment.ids,
                city: tracking.city,
                random: tracking.random,
            })
            .build(),
    )
}

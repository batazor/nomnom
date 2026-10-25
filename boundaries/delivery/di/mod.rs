//! Composition root процесса `delivery`: выбирает реализации портов и
//! собирает shaku-модули слайсов в итоговый [`ServiceModule`].
//!
//! Пока хранилище, SpiceDB и locator не выбраны, порты закрываются
//! in-memory адаптерами из `infrastructure::memory`.

mod http_worker;

pub use http_worker::HttpWorker;

use std::sync::Arc;
use std::time::{SystemTime, UNIX_EPOCH};

use shaku::{Component, Interface, module};

use crate::infrastructure::memory::{
    MemoryStore, SequentialIdGenerator, StaticAuthorization, StaticCourierLocator,
};
use crate::pkg::application::{Clock, IdGenerator, TransactionScope};
use crate::slices::courier::applications::authorization::CourierAuthorization;
use crate::slices::courier::applications::get_courier_current_delivery::CourierCurrentDeliveryReadModel;
use crate::slices::courier::applications::update_courier_location::CourierLocationStore;
use crate::slices::courier::domains::courier::CourierRepository;
use crate::slices::delivery::applications::authorization::DeliveryAuthorization;
use crate::slices::delivery::applications::courier_locator::CourierLocator;
use crate::slices::delivery::applications::list_available_delivery_offers::PendingDeliveryOffersReadModel;
use crate::slices::delivery::applications::outbox::DeliveryOutbox;
use crate::slices::delivery::applications::policy::AssignmentPolicy;
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
    #[shaku(default)]
    store: MemoryStore,
}

impl SharedStore for LocalStore {
    fn store(&self) -> &MemoryStore {
        &self.store
    }
}

/// Одна проверка прав на оба слайса.
pub trait SharedAuthorization: Interface {
    fn authorization(&self) -> &StaticAuthorization;
}

#[derive(Component)]
#[shaku(interface = SharedAuthorization)]
pub struct LocalAuthorization {
    #[shaku(default = StaticAuthorization::allow_all())]
    authorization: StaticAuthorization,
}

impl SharedAuthorization for LocalAuthorization {
    fn authorization(&self) -> &StaticAuthorization {
        &self.authorization
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
}

impl CourierPorts for LocalCourierPorts {
    fn authorization(&self) -> &dyn CourierAuthorization {
        self.authorization.authorization()
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
    #[shaku(default = StaticCourierLocator::new(Vec::new()))]
    locator: StaticCourierLocator,
    #[shaku(default = SequentialIdGenerator::new("delivery"))]
    ids: SequentialIdGenerator,
}

impl DeliveryPorts for LocalDeliveryPorts {
    fn authorization(&self) -> &dyn DeliveryAuthorization {
        self.authorization.authorization()
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

/// Собирает граф процесса: инфраструктура → модули слайсов → итоговый модуль.
#[must_use]
pub fn build() -> ServiceModule {
    assemble(StaticCourierLocator::new(Vec::new()))
}

/// Граф процесса с заданными кандидатами locator — для транспортных тестов.
#[cfg(test)]
pub(crate) fn build_with_locator(locator: StaticCourierLocator) -> ServiceModule {
    assemble(locator)
}

fn assemble(locator: StaticCourierLocator) -> ServiceModule {
    let infrastructure = Arc::new(
        InfrastructureModule::builder()
            .with_component_parameters::<LocalDeliveryPorts>(LocalDeliveryPortsParameters {
                locator,
                ids: SequentialIdGenerator::new("delivery"),
            })
            .build(),
    );
    let courier = Arc::new(CourierModule::builder(infrastructure.clone()).build());
    let delivery = Arc::new(DeliveryModule::builder(infrastructure).build());
    ServiceModule::builder(courier, delivery).build()
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
}

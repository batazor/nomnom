//! Граф use cases: [`InfrastructureModule`] → модули слайсов →
//! [`ServiceModule`]. Его собирают composition root `cmd/delivery`
//! ([`super::server`]) и тестовые графы [`build`] над in-memory адаптерами.

use std::sync::Arc;

use shaku::{HasComponent, module};

use crate::infrastructure::memory::{
    MemoryStore, RecordingRelationships, SequentialIdGenerator, StaticCourierLocator,
};
use crate::pkg::persistence::RepositoryError;
use crate::pkg::telemetry::usecase::UseCaseTelemetry;
use crate::slices::courier::di::{CourierModule, CourierUseCases};
use crate::slices::courier::infrastructure::observed_use_cases::ObservedCourierUseCases;
use crate::slices::delivery::applications::dispatch_deliveries::{
    DeliveryPosition, WaitingDeliveries,
};
use crate::slices::delivery::di::{DeliveryModule, DeliveryUseCases};
use crate::slices::delivery::domains::delivery::Delivery;
use crate::slices::delivery::infrastructure::observed_use_cases::ObservedDeliveryUseCases;

use super::infrastructure::{
    InfrastructureModule, ProcessAuthorization, ProcessStores, infrastructure, local_authorization,
};

module! {
    UseCaseModule {
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

/// Use cases процесса для транспортных адаптеров (они не импортируют shaku),
/// каждый за декоратором telemetry слайса (docs/observability.md#use-cases):
/// span `usecase <name>` и `nomnom.usecase.duration` на вызов.
pub struct ServiceModule {
    couriers: ObservedCourierUseCases,
    deliveries: ObservedDeliveryUseCases,
}

impl ServiceModule {
    #[must_use]
    pub fn couriers(&self) -> &dyn CourierUseCases {
        &self.couriers
    }

    #[must_use]
    pub fn deliveries(&self) -> &dyn DeliveryUseCases {
        &self.deliveries
    }
}

/// Модули слайсов над портами `infrastructure` и итоговый модуль use cases.
/// Instruments берутся у глобальных providers: telemetry запускается раньше.
#[must_use]
pub(super) fn service(infrastructure: Arc<InfrastructureModule>) -> ServiceModule {
    let courier = Arc::new(CourierModule::builder(infrastructure.clone()).build());
    let delivery = Arc::new(DeliveryModule::builder(infrastructure).build());
    let module = UseCaseModule::builder(courier, delivery).build();
    let telemetry = Arc::new(UseCaseTelemetry::global());
    let couriers: Arc<dyn CourierUseCases> = module.resolve();
    let deliveries: Arc<dyn DeliveryUseCases> = module.resolve();
    ServiceModule {
        couriers: ObservedCourierUseCases::new(couriers, Arc::clone(&telemetry)),
        deliveries: ObservedDeliveryUseCases::new(deliveries, telemetry),
    }
}

/// Выборка Dispatch Deliveries тестовых графов над in-memory хранилищем:
/// она есть только в PostgreSQL, поэтому Dispatch здесь — ошибка
/// зависимости. Тесты Dispatch передают свою подделку порта.
struct PostgresOnlyWaitingDeliveries;

impl WaitingDeliveries for PostgresOnlyWaitingDeliveries {
    fn list_waiting_without_pending_offer(
        &self,
        _after: Option<&DeliveryPosition>,
        _limit: usize,
    ) -> Result<Vec<Delivery>, RepositoryError> {
        Err(RepositoryError::Unavailable(
            "waiting deliveries are queried only in PostgreSQL".to_owned(),
        ))
    }
}

/// Роли in-memory хранилища тестового графа.
fn memory_stores(store: &Arc<MemoryStore>) -> ProcessStores {
    ProcessStores::with_waiting(store, Arc::new(PostgresOnlyWaitingDeliveries))
}

/// Собирает тестовый граф с локальной авторизацией.
#[must_use]
pub fn build() -> ServiceModule {
    build_with_authorization(Box::new(local_authorization()))
}

/// Собирает тестовый граф над in-memory хранилищем с выбранной авторизацией:
/// последовательные id и пустой статический locator — только для тестов.
#[must_use]
pub fn build_with_authorization(authorization: Box<dyn ProcessAuthorization>) -> ServiceModule {
    service(infrastructure(
        memory_stores(&Arc::new(MemoryStore::new())),
        Box::new(SequentialIdGenerator::new("delivery")),
        Box::new(StaticCourierLocator::new(Vec::new())),
        authorization,
        Box::new(RecordingRelationships::new()),
    ))
}

/// Тестовый граф над заданными хранилищами (PostgreSQL в integration-тестах)
/// с UUID id, пустым locator и выбранной авторизацией.
#[must_use]
pub fn build_over(
    stores: ProcessStores,
    authorization: Box<dyn ProcessAuthorization>,
) -> ServiceModule {
    service(infrastructure(
        stores,
        Box::new(crate::infrastructure::ids::UuidIdGenerator),
        Box::new(StaticCourierLocator::new(Vec::new())),
        authorization,
        Box::new(RecordingRelationships::new()),
    ))
}

/// Граф с заданными кандидатами locator — для транспортных тестов.
#[cfg(test)]
pub(crate) fn build_with_locator(locator: StaticCourierLocator) -> ServiceModule {
    service(infrastructure(
        memory_stores(&Arc::new(MemoryStore::new())),
        Box::new(SequentialIdGenerator::new("delivery")),
        Box::new(locator),
        Box::new(local_authorization()),
        Box::new(RecordingRelationships::new()),
    ))
}

/// Граф над данным in-memory хранилищем с locator процесса (сохранённые
/// позиции курьеров) и выбранными авторизацией и relationships — для
/// транспортных тестов, которые проверяют состояние хранилища.
#[cfg(test)]
pub(crate) fn build_over_memory(
    store: &Arc<MemoryStore>,
    authorization: Box<dyn ProcessAuthorization>,
    relationships: Box<dyn super::ProcessRelationships>,
) -> ServiceModule {
    service(infrastructure(
        memory_stores(store),
        Box::new(SequentialIdGenerator::new("delivery")),
        Box::new(crate::infrastructure::locator::StoredCourierLocator::new(
            Arc::clone(store) as _,
        )),
        authorization,
        relationships,
    ))
}

#[cfg(test)]
mod tests {
    use crate::infrastructure::memory::StaticAuthorization;
    use crate::pkg::application::Subject;
    use crate::slices::courier::applications::register_courier;
    use crate::slices::delivery::applications::{create_delivery, get_courier_current_delivery};

    use super::*;

    fn operator() -> Subject {
        Subject::User("operator-1".to_owned())
    }

    #[test]
    fn slices_share_one_store() {
        let module = build();
        let couriers = module.couriers();
        let deliveries = module.deliveries();

        couriers
            .register_courier(
                &operator(),
                register_courier::Input {
                    courier_id: "courier-1".to_owned(),
                },
            )
            .expect("courier registered");
        let current = deliveries
            .get_courier_current_delivery(
                &operator(),
                get_courier_current_delivery::Input {
                    courier_id: "courier-1".to_owned(),
                },
            )
            .expect("courier registered by one slice is visible to the other");
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

        assert!(
            module
                .couriers()
                .register_courier(
                    &operator(),
                    register_courier::Input {
                        courier_id: "courier-1".to_owned(),
                    },
                )
                .is_err()
        );
        assert!(
            module
                .deliveries()
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
}

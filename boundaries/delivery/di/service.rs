//! Граф use cases: [`InfrastructureModule`] → модули слайсов →
//! [`ServiceModule`]. Его собирают composition root `cmd/delivery`
//! ([`super::server`]), integration-тесты ([`build_over`]) и тестовые графы
//! над тестовым PostgreSQL.

use std::sync::Arc;

use shaku::{HasComponent, module};

#[cfg(test)]
use crate::infrastructure::memory::SequentialIdGenerator;
use crate::infrastructure::memory::{RecordingRelationships, StaticCourierLocator};
#[cfg(test)]
use crate::infrastructure::postgres::test_support::TestStore;
use crate::pkg::telemetry::usecase::UseCaseTelemetry;
use crate::slices::courier::di::{CourierModule, CourierUseCases};
use crate::slices::courier::infrastructure::observed_use_cases::ObservedCourierUseCases;
use crate::slices::delivery::di::{DeliveryModule, DeliveryUseCases};
use crate::slices::delivery::infrastructure::observed_use_cases::ObservedDeliveryUseCases;
use crate::slices::delivery::infrastructure::transitions::TransitionMetrics;

#[cfg(test)]
use super::infrastructure::local_authorization;
use super::infrastructure::{
    InfrastructureModule, ProcessAuthorization, ProcessStores, infrastructure,
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
        deliveries: ObservedDeliveryUseCases::new(
            deliveries,
            telemetry,
            TransitionMetrics::global(),
        ),
    }
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

/// Граф над тестовым PostgreSQL с заданными кандидатами locator, локальной
/// авторизацией и последовательными id — для транспортных тестов.
#[cfg(test)]
pub(crate) fn build_with_locator(
    store: &Arc<TestStore>,
    locator: StaticCourierLocator,
) -> ServiceModule {
    service(infrastructure(
        ProcessStores::over(store),
        Box::new(SequentialIdGenerator::new("delivery")),
        Box::new(locator),
        Box::new(local_authorization()),
        Box::new(RecordingRelationships::new()),
    ))
}

/// Граф над тестовым PostgreSQL с locator процесса (сохранённые позиции
/// курьеров) и выбранными авторизацией и relationships — для тестов, которые
/// проверяют состояние хранилища.
#[cfg(test)]
pub(crate) fn build_over_store(
    store: &Arc<TestStore>,
    authorization: Box<dyn ProcessAuthorization>,
    relationships: Box<dyn super::ProcessRelationships>,
) -> ServiceModule {
    service(infrastructure(
        ProcessStores::over(store),
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

    fn graph(authorization: Box<dyn ProcessAuthorization>) -> ServiceModule {
        build_over_store(
            &Arc::new(TestStore::new()),
            authorization,
            Box::new(RecordingRelationships::new()),
        )
    }

    #[test]
    fn slices_share_one_store() {
        let module = graph(Box::new(local_authorization()));
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
        let module = graph(Box::new(StaticAuthorization::unavailable()));

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

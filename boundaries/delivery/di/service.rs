//! Граф use cases: [`InfrastructureModule`] → модули слайсов →
//! [`ServiceModule`]. Его собирают composition root `cmd/delivery`
//! ([`super::server`]), integration-тесты ([`build_over`]) и тестовые графы
//! над тестовым PostgreSQL.

use std::sync::Arc;
use std::time::Duration;

use shaku::{HasComponent, module};

#[cfg(test)]
use crate::infrastructure::memory::SequentialIdGenerator;
use crate::infrastructure::memory::{
    FixedRandomSource, RecordingAssignment, RecordingPositionPublisher, RecordingRelationships,
    StaticCourierLocator,
};
#[cfg(test)]
use crate::infrastructure::repository::test_support::TestStore;
use crate::pkg::telemetry::usecase::UseCaseTelemetry;
use crate::slices::courier::di::{CourierModule, CourierUseCases};
use crate::slices::courier::infrastructure::observed_use_cases::ObservedCourierUseCases;
use crate::slices::delivery::di::{DeliveryModule, DeliveryUseCases};
use crate::slices::delivery::domains::delivery::{DeliveryEstimatePolicy, VirtualCity};
use crate::slices::delivery::infrastructure::observed_use_cases::ObservedDeliveryUseCases;
use crate::slices::delivery::infrastructure::transitions::TransitionMetrics;
use crate::slices::simulator::di::{SimulatorModule, SimulatorUseCases};

#[cfg(test)]
use super::infrastructure::local_authorization;
use super::infrastructure::{
    AssignmentAdapters, InfrastructureModule, ProcessAuthorization, ProcessStores, ProcessTracking,
    infrastructure,
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
    simulator: Arc<dyn SimulatorUseCases>,
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

    #[must_use]
    pub fn simulator(&self) -> &dyn SimulatorUseCases {
        &*self.simulator
    }
}

/// Модули слайсов над портами `infrastructure` и итоговый модуль use cases.
/// Instruments берутся у глобальных providers: telemetry запускается раньше.
#[must_use]
pub(super) fn service(infrastructure: Arc<InfrastructureModule>) -> ServiceModule {
    let courier = Arc::new(CourierModule::builder(infrastructure.clone()).build());
    let simulator = SimulatorModule::builder(infrastructure.clone()).build();
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
        simulator: simulator.resolve(),
    }
}

/// Live tracking тестовых графов: Virtual City 12 × 12 кварталов по 150 м
/// у позиций курьеров kind, запись каждой позиции, записывающий publisher и
/// фиксированная случайность. Не часть API крейта: снаружи тестовый граф
/// собирает только [`build_over`].
#[must_use]
pub(crate) fn test_tracking() -> ProcessTracking {
    ProcessTracking {
        city: VirtualCity::new(52.515, 13.385, 12, 150.0).expect("test virtual city"),
        persist_interval_seconds: 0,
        publisher: Box::new(RecordingPositionPublisher::new()),
        random: Box::new(FixedRandomSource::new((1..=24).collect())),
    }
}

/// Delivery Duration Estimate тестовых графов: значения kind (выборка 50,
/// 15m и 20m).
#[must_use]
pub(crate) fn test_estimate_policy() -> DeliveryEstimatePolicy {
    DeliveryEstimatePolicy::new(
        50,
        Duration::from_secs(15 * 60),
        Duration::from_secs(20 * 60),
    )
    .expect("test estimate policy")
}

/// Тестовый граф над заданными хранилищами (PostgreSQL в integration-тестах)
/// с UUID id, пустым locator, выбранной авторизацией и записывающим
/// Assignment Workflow.
#[must_use]
pub fn build_over(
    stores: ProcessStores,
    authorization: Box<dyn ProcessAuthorization>,
) -> ServiceModule {
    service(infrastructure(
        stores,
        AssignmentAdapters {
            ids: Box::new(crate::infrastructure::ids::UuidIdGenerator),
            locator: Box::new(StaticCourierLocator::new(Vec::new())),
            process: Arc::new(RecordingAssignment::new()),
        },
        authorization,
        Box::new(RecordingRelationships::new()),
        test_tracking(),
        test_estimate_policy(),
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
        AssignmentAdapters {
            ids: Box::new(SequentialIdGenerator::new("delivery")),
            locator: Box::new(locator),
            process: Arc::new(RecordingAssignment::new()),
        },
        Box::new(local_authorization()),
        Box::new(RecordingRelationships::new()),
        test_tracking(),
        test_estimate_policy(),
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
        AssignmentAdapters {
            ids: Box::new(SequentialIdGenerator::new("delivery")),
            locator: Box::new(crate::infrastructure::locator::StoredCourierLocator::new(
                Arc::clone(store) as _,
            )),
            process: Arc::new(RecordingAssignment::new()),
        },
        authorization,
        relationships,
        test_tracking(),
        test_estimate_policy(),
    ))
}

#[cfg(test)]
mod tests {
    use crate::infrastructure::memory::StaticAuthorization;
    use crate::pkg::authz::Subject;
    use crate::slices::courier::applications::register_courier;
    use crate::slices::delivery::applications::{
        assignment::create_delivery, execution::get_courier_current_delivery,
    };

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
                    name: "Timur".to_owned(),
                    market_id: "berlin".to_owned(),
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
                    market_id: "berlin".to_owned(),
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
                        name: "Timur".to_owned(),
                        market_id: "berlin".to_owned(),
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
                        market_id: "berlin".to_owned(),
                    },
                )
                .is_err()
        );
    }
}

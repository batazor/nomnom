//! Граф use cases: [`InfrastructureModule`] → модули слайсов →
//! [`ServiceModule`]. Его собирают composition root `cmd/delivery`
//! ([`super::server`]) и тестовые графы [`build`] над in-memory адаптерами.

use std::sync::Arc;

use shaku::{HasComponent, module};

use crate::infrastructure::memory::{MemoryStore, RecordingRelationships, StaticCourierLocator};
use crate::slices::courier::di::{CourierModule, CourierUseCases};
use crate::slices::delivery::di::{DeliveryModule, DeliveryUseCases};

use super::infrastructure::{
    InfrastructureModule, ProcessAuthorization, infrastructure, local_authorization,
};

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

/// Use cases процесса для транспортных адаптеров: они не импортируют shaku.
impl ServiceModule {
    #[must_use]
    pub fn couriers(&self) -> &dyn CourierUseCases {
        self.resolve_ref()
    }

    #[must_use]
    pub fn deliveries(&self) -> &dyn DeliveryUseCases {
        self.resolve_ref()
    }
}

/// Модули слайсов над портами `infrastructure` и итоговый модуль use cases.
#[must_use]
pub(super) fn service(infrastructure: Arc<InfrastructureModule>) -> ServiceModule {
    let courier = Arc::new(CourierModule::builder(infrastructure.clone()).build());
    let delivery = Arc::new(DeliveryModule::builder(infrastructure).build());
    ServiceModule::builder(courier, delivery).build()
}

/// Собирает тестовый граф с локальной авторизацией.
#[must_use]
pub fn build() -> ServiceModule {
    build_with_authorization(Box::new(local_authorization()))
}

/// Собирает тестовый граф над in-memory хранилищем с выбранной авторизацией.
#[must_use]
pub fn build_with_authorization(authorization: Box<dyn ProcessAuthorization>) -> ServiceModule {
    service(infrastructure(
        Arc::new(MemoryStore::new()),
        StaticCourierLocator::new(Vec::new()),
        authorization,
        Box::new(RecordingRelationships::new()),
    ))
}

/// Граф с заданными кандидатами locator — для транспортных тестов.
#[cfg(test)]
pub(crate) fn build_with_locator(locator: StaticCourierLocator) -> ServiceModule {
    service(infrastructure(
        Arc::new(MemoryStore::new()),
        locator,
        Box::new(local_authorization()),
        Box::new(RecordingRelationships::new()),
    ))
}

#[cfg(test)]
mod tests {
    use crate::infrastructure::memory::StaticAuthorization;
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

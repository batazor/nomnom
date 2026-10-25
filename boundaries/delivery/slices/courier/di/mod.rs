//! Shaku-модуль слайса `courier`.
//!
//! Домен и handlers shaku не импортируют. Реализации портов выбирает корневой `di`.

use std::sync::Arc;

use shaku::{Component, HasComponent, Interface, module};

use crate::pkg::application::{Clock, Subject};
use crate::slices::courier::applications::authorization::CourierAuthorization;
use crate::slices::courier::applications::error::CourierApplicationError;
use crate::slices::courier::applications::get_courier_current_delivery::{
    self, CourierCurrentDeliveryReadModel,
};
use crate::slices::courier::applications::relationships::CourierRelationshipWriter;
use crate::slices::courier::applications::update_courier_location::{self, CourierLocationStore};
use crate::slices::courier::applications::{register_courier, set_courier_availability};
use crate::slices::courier::domains::courier::CourierRepository;

/// Внешние порты слайса. Порты не требуют `Send + Sync`, поэтому shaku
/// хранит их за этим интерфейсом, а не как отдельные `dyn` компоненты.
pub trait CourierPorts: Interface {
    fn authorization(&self) -> &dyn CourierAuthorization;
    fn couriers(&self) -> &dyn CourierRepository;
    fn locations(&self) -> &dyn CourierLocationStore;
    fn current_deliveries(&self) -> &dyn CourierCurrentDeliveryReadModel;
    fn clock(&self) -> &dyn Clock;
    fn relationships(&self) -> &dyn CourierRelationshipWriter;
}

/// Модуль, через который корневой `di` передаёт слайсу его порты.
pub trait CourierDependencies: HasComponent<dyn CourierPorts> {}

pub trait CourierUseCases: Interface {
    fn register_courier(
        &self,
        subject: &Subject,
        input: register_courier::Input,
    ) -> Result<register_courier::Output, CourierApplicationError>;

    fn set_courier_availability(
        &self,
        subject: &Subject,
        input: set_courier_availability::Input,
    ) -> Result<set_courier_availability::Output, CourierApplicationError>;

    fn update_courier_location(
        &self,
        subject: &Subject,
        input: update_courier_location::Input,
    ) -> Result<update_courier_location::Output, CourierApplicationError>;

    fn get_courier_current_delivery(
        &self,
        subject: &Subject,
        input: get_courier_current_delivery::Input,
    ) -> Result<get_courier_current_delivery::Output, CourierApplicationError>;
}

#[derive(Component)]
#[shaku(interface = CourierUseCases)]
pub struct CourierUseCasesImpl {
    #[shaku(inject)]
    ports: Arc<dyn CourierPorts>,
}

impl CourierUseCases for CourierUseCasesImpl {
    fn register_courier(
        &self,
        subject: &Subject,
        input: register_courier::Input,
    ) -> Result<register_courier::Output, CourierApplicationError> {
        register_courier::Handler {
            authorization: self.ports.authorization(),
            couriers: self.ports.couriers(),
            relationships: self.ports.relationships(),
        }
        .execute(subject, input)
    }

    fn set_courier_availability(
        &self,
        subject: &Subject,
        input: set_courier_availability::Input,
    ) -> Result<set_courier_availability::Output, CourierApplicationError> {
        set_courier_availability::Handler {
            authorization: self.ports.authorization(),
            couriers: self.ports.couriers(),
        }
        .execute(subject, input)
    }

    fn update_courier_location(
        &self,
        subject: &Subject,
        input: update_courier_location::Input,
    ) -> Result<update_courier_location::Output, CourierApplicationError> {
        update_courier_location::Handler {
            authorization: self.ports.authorization(),
            couriers: self.ports.couriers(),
            locations: self.ports.locations(),
            clock: self.ports.clock(),
        }
        .execute(subject, input)
    }

    fn get_courier_current_delivery(
        &self,
        subject: &Subject,
        input: get_courier_current_delivery::Input,
    ) -> Result<get_courier_current_delivery::Output, CourierApplicationError> {
        get_courier_current_delivery::Handler {
            authorization: self.ports.authorization(),
            couriers: self.ports.couriers(),
            current_deliveries: self.ports.current_deliveries(),
        }
        .execute(subject, input)
    }
}

module! {
    pub CourierModule {
        components = [CourierUseCasesImpl],
        providers = [],

        use dyn CourierDependencies {
            components = [dyn CourierPorts],
            providers = []
        }
    }
}

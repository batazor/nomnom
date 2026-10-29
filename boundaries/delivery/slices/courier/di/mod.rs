//! Shaku-модуль слайса `courier`.
//!
//! Домен и handlers shaku не импортируют. Реализации портов выбирает корневой `di`.

use std::sync::Arc;

use shaku::{Component, HasComponent, Interface, module};

use crate::pkg::application::{Clock, Subject};
use crate::slices::courier::applications::authorization::CourierAuthorization;
use crate::slices::courier::applications::candidate_wake::CandidateWake;
use crate::slices::courier::applications::error::CourierApplicationError;
use crate::slices::courier::applications::relationships::CourierRelationshipWriter;
use crate::slices::courier::applications::update_courier_location::{
    self, ActiveTrackingChannel, CourierLocationStore, CourierPositionPublisher,
};
use crate::slices::courier::applications::{
    register_courier, retire_courier, set_courier_availability,
};
use crate::slices::courier::domains::courier::CourierRepository;

/// Внешние порты слайса. Порты не требуют `Send + Sync`, поэтому shaku
/// хранит их за этим интерфейсом, а не как отдельные `dyn` компоненты.
pub trait CourierPorts: Interface {
    fn authorization(&self) -> &dyn CourierAuthorization;
    fn couriers(&self) -> &dyn CourierRepository;
    fn locations(&self) -> &dyn CourierLocationStore;
    fn channels(&self) -> &dyn ActiveTrackingChannel;
    fn publisher(&self) -> &dyn CourierPositionPublisher;
    fn clock(&self) -> &dyn Clock;
    fn relationships(&self) -> &dyn CourierRelationshipWriter;
    /// Courier search of the Market woken by an available courier (FR-706).
    fn wake(&self) -> &dyn CandidateWake;
}

/// Числовые параметры слайса; значения задаёт корневой `di`.
pub trait CourierSettings: Interface {
    /// `TRACKING__PERSIST_INTERVAL` in seconds.
    fn persist_interval_seconds(&self) -> i64;
}

/// Модуль, через который корневой `di` передаёт слайсу порты и настройки.
pub trait CourierDependencies:
    HasComponent<dyn CourierPorts> + HasComponent<dyn CourierSettings>
{
}

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

    fn retire_courier(
        &self,
        subject: &Subject,
        input: retire_courier::Input,
    ) -> Result<retire_courier::Output, CourierApplicationError>;

    fn update_courier_location(
        &self,
        subject: &Subject,
        input: update_courier_location::Input,
    ) -> Result<update_courier_location::Output, CourierApplicationError>;
}

#[derive(Component)]
#[shaku(interface = CourierUseCases)]
pub struct CourierUseCasesImpl {
    #[shaku(inject)]
    ports: Arc<dyn CourierPorts>,
    #[shaku(inject)]
    settings: Arc<dyn CourierSettings>,
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
            wake: self.ports.wake(),
        }
        .execute(subject, input)
    }

    fn retire_courier(
        &self,
        subject: &Subject,
        input: retire_courier::Input,
    ) -> Result<retire_courier::Output, CourierApplicationError> {
        retire_courier::Handler {
            authorization: self.ports.authorization(),
            couriers: self.ports.couriers(),
            clock: self.ports.clock(),
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
            channels: self.ports.channels(),
            publisher: self.ports.publisher(),
            wake: self.ports.wake(),
            clock: self.ports.clock(),
            persist_interval_seconds: self.settings.persist_interval_seconds(),
        }
        .execute(subject, input)
    }
}

module! {
    pub CourierModule {
        components = [CourierUseCasesImpl],
        providers = [],

        use dyn CourierDependencies {
            components = [dyn CourierPorts, dyn CourierSettings],
            providers = []
        }
    }
}

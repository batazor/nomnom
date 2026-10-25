//! Shaku-модуль слайса `delivery`.
//!
//! Домен и handlers shaku не импортируют. Реализации портов и значения политики выбирает
//! корневой `di`.

use std::sync::Arc;

use shaku::{Component, HasComponent, Interface, module};

use crate::pkg::application::{Clock, IdGenerator, ScopedUnitOfWork, Subject, TransactionScope};
use crate::slices::courier::applications::assignment::CourierAssignment;
use crate::slices::courier::applications::authorization::CourierAuthorization;
use crate::slices::delivery::applications::authorization::DeliveryAuthorization;
use crate::slices::delivery::applications::courier_locator::CourierLocator;
use crate::slices::delivery::applications::error::DeliveryApplicationError;
use crate::slices::delivery::applications::get_courier_current_delivery::{
    self, CourierCurrentDeliveryReadModel,
};
use crate::slices::delivery::applications::list_available_delivery_offers::{
    self, PendingDeliveryOffersReadModel,
};
use crate::slices::delivery::applications::outbox::DeliveryOutbox;
use crate::slices::delivery::applications::policy::AssignmentPolicy;
use crate::slices::delivery::applications::relationships::DeliveryRelationshipWriter;
use crate::slices::delivery::applications::{
    accept_delivery_offer, cancel_delivery, confirm_delivery, confirm_pickup, create_delivery,
    find_courier, offer_delivery, reassign_courier, reject_delivery_offer, replan_delivery,
    report_delivery_failure,
};
use crate::slices::delivery::domains::delivery::{DeliveryOfferRepository, DeliveryRepository};

/// Транзакционная роль хранилища слайса: Unit of Work, репозитории
/// `Delivery` и offer, занятость курьера (порт слайса `courier`) и Outbox.
/// Одно хранилище, поэтому Unit of Work охватывает все эти записи.
pub trait TransactionalStore:
    TransactionScope
    + DeliveryRepository
    + DeliveryOfferRepository
    + CourierAssignment
    + DeliveryOutbox
    + Send
    + Sync
{
}

impl<T> TransactionalStore for T where
    T: TransactionScope
        + DeliveryRepository
        + DeliveryOfferRepository
        + CourierAssignment
        + DeliveryOutbox
        + Send
        + Sync
{
}

/// Read models запросов слайса.
pub trait DeliveryReadModels:
    PendingDeliveryOffersReadModel + CourierCurrentDeliveryReadModel + Send + Sync
{
}

impl<T> DeliveryReadModels for T where
    T: PendingDeliveryOffersReadModel + CourierCurrentDeliveryReadModel + Send + Sync
{
}

/// Внешние порты слайса. Порты не требуют `Send + Sync`, поэтому shaku
/// хранит их за этим интерфейсом, а не как отдельные `dyn` компоненты.
pub trait DeliveryPorts: Interface {
    fn authorization(&self) -> &dyn DeliveryAuthorization;
    /// Право курьера на Get Courier Current Delivery проверяет слайс `courier`.
    fn courier_authorization(&self) -> &dyn CourierAuthorization;
    fn store(&self) -> &dyn TransactionalStore;
    fn read_models(&self) -> &dyn DeliveryReadModels;
    fn locator(&self) -> &dyn CourierLocator;
    fn clock(&self) -> &dyn Clock;
    fn ids(&self) -> &dyn IdGenerator;
    fn relationships(&self) -> &dyn DeliveryRelationshipWriter;
}

/// Числовые параметры назначения; значения задаёт корневой `di`.
pub trait DeliverySettings: Interface {
    fn assignment_policy(&self) -> AssignmentPolicy;
}

/// Модуль, через который корневой `di` передаёт слайсу порты и настройки.
pub trait DeliveryDependencies:
    HasComponent<dyn DeliveryPorts> + HasComponent<dyn DeliverySettings>
{
}

pub trait DeliveryUseCases: Interface {
    fn create_delivery(
        &self,
        subject: &Subject,
        input: create_delivery::Input,
    ) -> Result<create_delivery::Output, DeliveryApplicationError>;

    fn find_courier(
        &self,
        subject: &Subject,
        input: find_courier::Input,
    ) -> Result<find_courier::Output, DeliveryApplicationError>;

    fn offer_delivery(
        &self,
        subject: &Subject,
        input: offer_delivery::Input,
    ) -> Result<offer_delivery::Output, DeliveryApplicationError>;

    fn list_available_delivery_offers(
        &self,
        subject: &Subject,
        input: list_available_delivery_offers::Input,
    ) -> Result<list_available_delivery_offers::Output, DeliveryApplicationError>;

    fn get_courier_current_delivery(
        &self,
        subject: &Subject,
        input: get_courier_current_delivery::Input,
    ) -> Result<get_courier_current_delivery::Output, DeliveryApplicationError>;

    fn accept_delivery_offer(
        &self,
        subject: &Subject,
        input: accept_delivery_offer::Input,
    ) -> Result<accept_delivery_offer::Output, DeliveryApplicationError>;

    fn reject_delivery_offer(
        &self,
        subject: &Subject,
        input: reject_delivery_offer::Input,
    ) -> Result<reject_delivery_offer::Output, DeliveryApplicationError>;

    fn reassign_courier(
        &self,
        subject: &Subject,
        input: reassign_courier::Input,
    ) -> Result<reassign_courier::Output, DeliveryApplicationError>;

    fn confirm_pickup(
        &self,
        subject: &Subject,
        input: confirm_pickup::Input,
    ) -> Result<confirm_pickup::Output, DeliveryApplicationError>;

    fn confirm_delivery(
        &self,
        subject: &Subject,
        input: confirm_delivery::Input,
    ) -> Result<confirm_delivery::Output, DeliveryApplicationError>;

    fn report_delivery_failure(
        &self,
        subject: &Subject,
        input: report_delivery_failure::Input,
    ) -> Result<report_delivery_failure::Output, DeliveryApplicationError>;

    fn cancel_delivery(
        &self,
        subject: &Subject,
        input: cancel_delivery::Input,
    ) -> Result<cancel_delivery::Output, DeliveryApplicationError>;

    fn replan_delivery(
        &self,
        subject: &Subject,
        input: replan_delivery::Input,
    ) -> Result<replan_delivery::Output, DeliveryApplicationError>;
}

#[derive(Component)]
#[shaku(interface = DeliveryUseCases)]
pub struct DeliveryUseCasesImpl {
    #[shaku(inject)]
    ports: Arc<dyn DeliveryPorts>,
    #[shaku(inject)]
    settings: Arc<dyn DeliverySettings>,
}

impl DeliveryUseCasesImpl {
    fn unit_of_work(&self) -> ScopedUnitOfWork<'_> {
        ScopedUnitOfWork::new(self.ports.store())
    }
}

impl DeliveryUseCases for DeliveryUseCasesImpl {
    fn create_delivery(
        &self,
        subject: &Subject,
        input: create_delivery::Input,
    ) -> Result<create_delivery::Output, DeliveryApplicationError> {
        create_delivery::Handler {
            authorization: self.ports.authorization(),
            unit_of_work: &self.unit_of_work(),
            deliveries: self.ports.store(),
            outbox: self.ports.store(),
            ids: self.ports.ids(),
            clock: self.ports.clock(),
        }
        .execute(subject, input)
    }

    fn find_courier(
        &self,
        subject: &Subject,
        input: find_courier::Input,
    ) -> Result<find_courier::Output, DeliveryApplicationError> {
        find_courier::Handler {
            authorization: self.ports.authorization(),
            deliveries: self.ports.store(),
            couriers: self.ports.store(),
            offers: self.ports.store(),
            locator: self.ports.locator(),
            policy: self.settings.assignment_policy(),
        }
        .execute(subject, input)
    }

    fn offer_delivery(
        &self,
        subject: &Subject,
        input: offer_delivery::Input,
    ) -> Result<offer_delivery::Output, DeliveryApplicationError> {
        offer_delivery::Handler {
            authorization: self.ports.authorization(),
            deliveries: self.ports.store(),
            couriers: self.ports.store(),
            offers: self.ports.store(),
            ids: self.ports.ids(),
            clock: self.ports.clock(),
            policy: self.settings.assignment_policy(),
        }
        .execute(subject, input)
    }

    fn list_available_delivery_offers(
        &self,
        subject: &Subject,
        input: list_available_delivery_offers::Input,
    ) -> Result<list_available_delivery_offers::Output, DeliveryApplicationError> {
        list_available_delivery_offers::Handler {
            authorization: self.ports.authorization(),
            pending_offers: self.ports.read_models(),
            clock: self.ports.clock(),
        }
        .execute(subject, input)
    }

    fn get_courier_current_delivery(
        &self,
        subject: &Subject,
        input: get_courier_current_delivery::Input,
    ) -> Result<get_courier_current_delivery::Output, DeliveryApplicationError> {
        get_courier_current_delivery::Handler {
            authorization: self.ports.courier_authorization(),
            couriers: self.ports.store(),
            current_deliveries: self.ports.read_models(),
        }
        .execute(subject, input)
    }

    fn accept_delivery_offer(
        &self,
        subject: &Subject,
        input: accept_delivery_offer::Input,
    ) -> Result<accept_delivery_offer::Output, DeliveryApplicationError> {
        accept_delivery_offer::Handler {
            authorization: self.ports.authorization(),
            unit_of_work: &self.unit_of_work(),
            offers: self.ports.store(),
            deliveries: self.ports.store(),
            couriers: self.ports.store(),
            outbox: self.ports.store(),
            locator: self.ports.locator(),
            clock: self.ports.clock(),
            policy: self.settings.assignment_policy(),
            relationships: self.ports.relationships(),
        }
        .execute(subject, input)
    }

    fn reject_delivery_offer(
        &self,
        subject: &Subject,
        input: reject_delivery_offer::Input,
    ) -> Result<reject_delivery_offer::Output, DeliveryApplicationError> {
        reject_delivery_offer::Handler {
            authorization: self.ports.authorization(),
            unit_of_work: &self.unit_of_work(),
            offers: self.ports.store(),
            outbox: self.ports.store(),
            clock: self.ports.clock(),
        }
        .execute(subject, input)
    }

    fn reassign_courier(
        &self,
        subject: &Subject,
        input: reassign_courier::Input,
    ) -> Result<reassign_courier::Output, DeliveryApplicationError> {
        reassign_courier::Handler {
            authorization: self.ports.authorization(),
            unit_of_work: &self.unit_of_work(),
            deliveries: self.ports.store(),
            couriers: self.ports.store(),
            outbox: self.ports.store(),
            relationships: self.ports.relationships(),
        }
        .execute(subject, input)
    }

    fn confirm_pickup(
        &self,
        subject: &Subject,
        input: confirm_pickup::Input,
    ) -> Result<confirm_pickup::Output, DeliveryApplicationError> {
        confirm_pickup::Handler {
            authorization: self.ports.authorization(),
            unit_of_work: &self.unit_of_work(),
            deliveries: self.ports.store(),
            outbox: self.ports.store(),
        }
        .execute(subject, input)
    }

    fn confirm_delivery(
        &self,
        subject: &Subject,
        input: confirm_delivery::Input,
    ) -> Result<confirm_delivery::Output, DeliveryApplicationError> {
        confirm_delivery::Handler {
            authorization: self.ports.authorization(),
            unit_of_work: &self.unit_of_work(),
            deliveries: self.ports.store(),
            couriers: self.ports.store(),
            outbox: self.ports.store(),
            relationships: self.ports.relationships(),
        }
        .execute(subject, input)
    }

    fn report_delivery_failure(
        &self,
        subject: &Subject,
        input: report_delivery_failure::Input,
    ) -> Result<report_delivery_failure::Output, DeliveryApplicationError> {
        report_delivery_failure::Handler {
            authorization: self.ports.authorization(),
            unit_of_work: &self.unit_of_work(),
            deliveries: self.ports.store(),
            couriers: self.ports.store(),
            outbox: self.ports.store(),
            relationships: self.ports.relationships(),
        }
        .execute(subject, input)
    }

    fn cancel_delivery(
        &self,
        subject: &Subject,
        input: cancel_delivery::Input,
    ) -> Result<cancel_delivery::Output, DeliveryApplicationError> {
        cancel_delivery::Handler {
            authorization: self.ports.authorization(),
            unit_of_work: &self.unit_of_work(),
            deliveries: self.ports.store(),
            couriers: self.ports.store(),
            outbox: self.ports.store(),
            relationships: self.ports.relationships(),
        }
        .execute(subject, input)
    }

    fn replan_delivery(
        &self,
        subject: &Subject,
        input: replan_delivery::Input,
    ) -> Result<replan_delivery::Output, DeliveryApplicationError> {
        replan_delivery::Handler {
            authorization: self.ports.authorization(),
            unit_of_work: &self.unit_of_work(),
            deliveries: self.ports.store(),
            couriers: self.ports.store(),
            offers: self.ports.store(),
            outbox: self.ports.store(),
            locator: self.ports.locator(),
            ids: self.ports.ids(),
            clock: self.ports.clock(),
            policy: self.settings.assignment_policy(),
            relationships: self.ports.relationships(),
        }
        .execute(subject, input)
    }
}

module! {
    pub DeliveryModule {
        components = [DeliveryUseCasesImpl],
        providers = [],

        use dyn DeliveryDependencies {
            components = [dyn DeliveryPorts, dyn DeliverySettings],
            providers = []
        }
    }
}

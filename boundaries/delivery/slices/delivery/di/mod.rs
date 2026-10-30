//! Shaku-модуль слайса `delivery`.
//!
//! Домен и handlers shaku не импортируют. Реализации портов и значения политики выбирает
//! корневой `di`.

use std::sync::Arc;

use shaku::{Component, HasComponent, Interface, module};

use crate::pkg::authz::Subject;
use crate::pkg::unit_of_work::{ScopedUnitOfWork, TransactionScope};
use crate::pkg::usecase::{Clock, IdGenerator};
use crate::slices::courier::applications::assignment::CourierAssignment;
use crate::slices::courier::applications::authorization::CourierAuthorization;
use crate::slices::courier::applications::candidate_wake::CandidateWake;
use crate::slices::delivery::applications::assignment_process::AssignmentProcess;
use crate::slices::delivery::applications::authorization::DeliveryAuthorization;
use crate::slices::delivery::applications::courier_locator::CourierLocator;
use crate::slices::delivery::applications::duration::estimate_delivery_duration::{
    self, DeliveryDurationSamples,
};
use crate::slices::delivery::applications::duration::record_delivery_duration::{
    self, DeliveryDurationStatistics, DeliveryInbox,
};
use crate::slices::delivery::applications::error::DeliveryApplicationError;
use crate::slices::delivery::applications::execution::get_courier_current_delivery::{
    self, CourierCurrentDeliveryReadModel,
};
use crate::slices::delivery::applications::get_courier_pool::{self, CourierPoolReadModel};
use crate::slices::delivery::applications::offer::list_available_delivery_offers::{
    self, PendingDeliveryOffersReadModel,
};
use crate::slices::delivery::applications::offers_of_delivery::OffersOfDelivery;
use crate::slices::delivery::applications::outbox::DeliveryOutbox;
use crate::slices::delivery::applications::policy::AssignmentPolicy;
use crate::slices::delivery::applications::relationships::DeliveryRelationshipWriter;
use crate::slices::delivery::applications::tracking::RandomSource;
use crate::slices::delivery::applications::tracking::get_order_delivery::{
    self, OrderDeliveryReadModel,
};
use crate::slices::delivery::applications::{
    assignment::advance_assignment, assignment::create_delivery, assignment::find_courier,
    assignment::reassign_courier, execution::cancel_delivery, execution::confirm_delivery,
    execution::confirm_pickup, execution::report_delivery_failure, offer::accept_delivery_offer,
    offer::expire_delivery_offer, offer::offer_delivery, offer::offer_delivery_to_courier,
    offer::reject_delivery_offer,
};
use crate::slices::delivery::domains::delivery::{
    DeliveryEstimatePolicy, DeliveryOfferRepository, DeliveryRepository, VirtualCity,
};

/// Транзакционная роль хранилища слайса: Unit of Work, репозитории
/// `Delivery` и offer, занятость курьера (порт слайса `courier`), Outbox,
/// Inbox и Delivery Duration Statistics. Одно хранилище, поэтому Unit of
/// Work охватывает все эти записи.
pub trait TransactionalStore:
    TransactionScope
    + DeliveryRepository
    + DeliveryOfferRepository
    + OffersOfDelivery
    + CourierAssignment
    + DeliveryOutbox
    + DeliveryInbox
    + DeliveryDurationStatistics
    + Send
    + Sync
{
}

impl<T> TransactionalStore for T where
    T: TransactionScope
        + DeliveryRepository
        + DeliveryOfferRepository
        + OffersOfDelivery
        + CourierAssignment
        + DeliveryOutbox
        + DeliveryInbox
        + DeliveryDurationStatistics
        + Send
        + Sync
{
}

/// Read models запросов слайса.
pub trait DeliveryReadModels:
    PendingDeliveryOffersReadModel
    + CourierCurrentDeliveryReadModel
    + CourierPoolReadModel
    + OrderDeliveryReadModel
    + DeliveryDurationSamples
    + Send
    + Sync
{
}

impl<T> DeliveryReadModels for T where
    T: PendingDeliveryOffersReadModel
        + CourierCurrentDeliveryReadModel
        + CourierPoolReadModel
        + OrderDeliveryReadModel
        + DeliveryDurationSamples
        + Send
        + Sync
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
    /// Virtual City of the tracking (FR-658).
    fn city(&self) -> &VirtualCity;
    /// Random source of the points and channel of a new Delivery.
    fn random(&self) -> &dyn RandomSource;
    /// Assignment Workflow of a Delivery: start and wake after commit.
    fn assignment(&self) -> &dyn AssignmentProcess;
    /// Courier search of a Market woken by a released courier (FR-706).
    fn candidates(&self) -> &dyn CandidateWake;
}

/// Числовые параметры назначения; значения задаёт корневой `di`.
pub trait DeliverySettings: Interface {
    fn assignment_policy(&self) -> AssignmentPolicy;
    /// Delivery Duration Estimate (FR-691): sample size and baselines.
    fn estimate_policy(&self) -> DeliveryEstimatePolicy;
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

    fn offer_delivery_to_courier(
        &self,
        subject: &Subject,
        input: offer_delivery_to_courier::Input,
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

    fn get_courier_pool(
        &self,
        subject: &Subject,
        input: get_courier_pool::Input,
    ) -> Result<get_courier_pool::Output, DeliveryApplicationError>;

    fn get_order_delivery(
        &self,
        subject: &Subject,
        input: get_order_delivery::Input,
    ) -> Result<get_order_delivery::Output, DeliveryApplicationError>;

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

    fn estimate_delivery_duration(
        &self,
        subject: &Subject,
        input: estimate_delivery_duration::Input,
    ) -> Result<estimate_delivery_duration::Output, DeliveryApplicationError>;

    /// Consumer of the Delivery's own facts: no caller subject.
    fn record_delivery_duration(
        &self,
        input: record_delivery_duration::Input,
    ) -> Result<record_delivery_duration::Output, DeliveryApplicationError>;

    /// Step of the Assignment Workflow: no caller subject.
    fn advance_assignment(
        &self,
        input: advance_assignment::Input,
    ) -> Result<advance_assignment::Output, DeliveryApplicationError>;

    /// Offer timer of the Assignment Workflow: no caller subject.
    fn expire_delivery_offer(
        &self,
        input: expire_delivery_offer::Input,
    ) -> Result<expire_delivery_offer::Output, DeliveryApplicationError>;
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
            city: self.ports.city(),
            random: self.ports.random(),
            assignment: self.ports.assignment(),
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
            offers_of: self.ports.store(),
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
            unit_of_work: &self.unit_of_work(),
            deliveries: self.ports.store(),
            couriers: self.ports.store(),
            offers: self.ports.store(),
            offers_of: self.ports.store(),
            outbox: self.ports.store(),
            ids: self.ports.ids(),
            clock: self.ports.clock(),
            policy: self.settings.assignment_policy(),
        }
        .execute(subject, input)
    }

    fn offer_delivery_to_courier(
        &self,
        subject: &Subject,
        input: offer_delivery_to_courier::Input,
    ) -> Result<offer_delivery::Output, DeliveryApplicationError> {
        offer_delivery_to_courier::Handler {
            authorization: self.ports.authorization(),
            unit_of_work: &self.unit_of_work(),
            deliveries: self.ports.store(),
            couriers: self.ports.store(),
            offers: self.ports.store(),
            offers_of: self.ports.store(),
            outbox: self.ports.store(),
            ids: self.ports.ids(),
            clock: self.ports.clock(),
            policy: self.settings.assignment_policy(),
            assignment: self.ports.assignment(),
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

    fn get_courier_pool(
        &self,
        subject: &Subject,
        input: get_courier_pool::Input,
    ) -> Result<get_courier_pool::Output, DeliveryApplicationError> {
        get_courier_pool::Handler {
            authorization: self.ports.courier_authorization(),
            pool: self.ports.read_models(),
            clock: self.ports.clock(),
        }
        .execute(subject, input)
    }

    fn get_order_delivery(
        &self,
        subject: &Subject,
        input: get_order_delivery::Input,
    ) -> Result<get_order_delivery::Output, DeliveryApplicationError> {
        get_order_delivery::Handler {
            authorization: self.ports.authorization(),
            deliveries: self.ports.read_models(),
            city: self.ports.city(),
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
            offers_of: self.ports.store(),
            deliveries: self.ports.store(),
            couriers: self.ports.store(),
            outbox: self.ports.store(),
            locator: self.ports.locator(),
            clock: self.ports.clock(),
            policy: self.settings.assignment_policy(),
            relationships: self.ports.relationships(),
            assignment: self.ports.assignment(),
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
            assignment: self.ports.assignment(),
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
            assignment: self.ports.assignment(),
            candidates: self.ports.candidates(),
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
            clock: self.ports.clock(),
            assignment: self.ports.assignment(),
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
            candidates: self.ports.candidates(),
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
            assignment: self.ports.assignment(),
            candidates: self.ports.candidates(),
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
            assignment: self.ports.assignment(),
            candidates: self.ports.candidates(),
        }
        .execute(subject, input)
    }

    fn estimate_delivery_duration(
        &self,
        subject: &Subject,
        input: estimate_delivery_duration::Input,
    ) -> Result<estimate_delivery_duration::Output, DeliveryApplicationError> {
        estimate_delivery_duration::Handler {
            authorization: self.ports.authorization(),
            samples: self.ports.read_models(),
            policy: self.settings.estimate_policy(),
        }
        .execute(subject, input)
    }

    fn record_delivery_duration(
        &self,
        input: record_delivery_duration::Input,
    ) -> Result<record_delivery_duration::Output, DeliveryApplicationError> {
        record_delivery_duration::Handler {
            unit_of_work: &self.unit_of_work(),
            inbox: self.ports.store(),
            statistics: self.ports.store(),
            deliveries: self.ports.store(),
            policy: self.settings.estimate_policy(),
        }
        .execute(input)
    }

    fn advance_assignment(
        &self,
        input: advance_assignment::Input,
    ) -> Result<advance_assignment::Output, DeliveryApplicationError> {
        advance_assignment::Handler {
            unit_of_work: &self.unit_of_work(),
            deliveries: self.ports.store(),
            couriers: self.ports.store(),
            offers: self.ports.store(),
            offers_of: self.ports.store(),
            outbox: self.ports.store(),
            locator: self.ports.locator(),
            ids: self.ports.ids(),
            clock: self.ports.clock(),
            policy: self.settings.assignment_policy(),
        }
        .execute(input)
    }

    fn expire_delivery_offer(
        &self,
        input: expire_delivery_offer::Input,
    ) -> Result<expire_delivery_offer::Output, DeliveryApplicationError> {
        expire_delivery_offer::Handler {
            unit_of_work: &self.unit_of_work(),
            offers: self.ports.store(),
            outbox: self.ports.store(),
            clock: self.ports.clock(),
        }
        .execute(input)
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

//! Use case telemetry of the `delivery` slice (docs/observability.md#use-cases):
//! a decorator of [`DeliveryUseCases`] that the composition root puts in
//! front of the use cases, so transports and background jobs are observed
//! without changing the handlers. The slice owns the use case names and the
//! closed set of `error.type`:
//!
//! - `rejected`: `permission_denied`, `invalid_argument`, `not_found`,
//!   `already_exists`, `failed_precondition`;
//! - `error`: `concurrent_modification`, `unavailable`, `transaction`,
//!   `internal`.

use std::sync::Arc;

use crate::pkg::application::Subject;
use crate::pkg::telemetry::usecase::{Failure, UseCase, UseCaseTelemetry};
use crate::slices::delivery::applications::error::DeliveryApplicationError;
use crate::slices::delivery::applications::{
    accept_delivery_offer, cancel_delivery, confirm_delivery, confirm_pickup, create_delivery,
    expire_delivery_offers, find_courier, get_courier_current_delivery,
    list_available_delivery_offers, offer_delivery, reassign_courier, reject_delivery_offer,
    replan_delivery, report_delivery_failure,
};
use crate::slices::delivery::di::DeliveryUseCases;

pub const CREATE_DELIVERY: UseCase = UseCase::command("create_delivery");
pub const FIND_COURIER: UseCase = UseCase::query("find_courier");
pub const OFFER_DELIVERY: UseCase = UseCase::command("offer_delivery");
pub const LIST_AVAILABLE_DELIVERY_OFFERS: UseCase =
    UseCase::query("list_available_delivery_offers");
pub const GET_COURIER_CURRENT_DELIVERY: UseCase = UseCase::query("get_courier_current_delivery");
pub const ACCEPT_DELIVERY_OFFER: UseCase = UseCase::command("accept_delivery_offer");
pub const REJECT_DELIVERY_OFFER: UseCase = UseCase::command("reject_delivery_offer");
pub const REASSIGN_COURIER: UseCase = UseCase::command("reassign_courier");
pub const CONFIRM_PICKUP: UseCase = UseCase::command("confirm_pickup");
pub const CONFIRM_DELIVERY: UseCase = UseCase::command("confirm_delivery");
pub const REPORT_DELIVERY_FAILURE: UseCase = UseCase::command("report_delivery_failure");
pub const CANCEL_DELIVERY: UseCase = UseCase::command("cancel_delivery");
pub const REPLAN_DELIVERY: UseCase = UseCase::command("replan_delivery");
pub const EXPIRE_DELIVERY_OFFERS: UseCase = UseCase::command("expire_delivery_offers");

/// `nomnom.outcome` and `error.type` of a failed call.
#[must_use]
pub fn classify(error: &DeliveryApplicationError) -> Failure {
    match error {
        DeliveryApplicationError::PermissionDenied => Failure::Rejected("permission_denied"),
        DeliveryApplicationError::InvalidRequest(_) => Failure::Rejected("invalid_argument"),
        DeliveryApplicationError::DeliveryNotFound(_)
        | DeliveryApplicationError::DeliveryForOrderNotFound(_)
        | DeliveryApplicationError::CourierNotFound(_)
        | DeliveryApplicationError::DeliveryOfferNotFound(_) => Failure::Rejected("not_found"),
        DeliveryApplicationError::DeliveryAlreadyExists(_) => Failure::Rejected("already_exists"),
        DeliveryApplicationError::DeliveryPrecondition(_)
        | DeliveryApplicationError::CourierPrecondition(_)
        | DeliveryApplicationError::OfferPrecondition(_)
        | DeliveryApplicationError::CourierPositionUnknown(_) => {
            Failure::Rejected("failed_precondition")
        }
        DeliveryApplicationError::ConcurrentModification => {
            Failure::Error("concurrent_modification")
        }
        DeliveryApplicationError::DependencyUnavailable(_) => Failure::Error("unavailable"),
        DeliveryApplicationError::Transaction(_) => Failure::Error("transaction"),
        DeliveryApplicationError::Internal(_) => Failure::Error("internal"),
    }
}

/// [`DeliveryUseCases`] observed by [`UseCaseTelemetry`]; results are
/// returned unchanged.
pub struct ObservedDeliveryUseCases {
    inner: Arc<dyn DeliveryUseCases>,
    telemetry: Arc<UseCaseTelemetry>,
}

impl ObservedDeliveryUseCases {
    #[must_use]
    pub fn new(inner: Arc<dyn DeliveryUseCases>, telemetry: Arc<UseCaseTelemetry>) -> Self {
        Self { inner, telemetry }
    }
}

impl DeliveryUseCases for ObservedDeliveryUseCases {
    fn create_delivery(
        &self,
        subject: &Subject,
        input: create_delivery::Input,
    ) -> Result<create_delivery::Output, DeliveryApplicationError> {
        self.telemetry.observe(CREATE_DELIVERY, classify, || {
            self.inner.create_delivery(subject, input)
        })
    }

    fn find_courier(
        &self,
        subject: &Subject,
        input: find_courier::Input,
    ) -> Result<find_courier::Output, DeliveryApplicationError> {
        self.telemetry.observe(FIND_COURIER, classify, || {
            self.inner.find_courier(subject, input)
        })
    }

    fn offer_delivery(
        &self,
        subject: &Subject,
        input: offer_delivery::Input,
    ) -> Result<offer_delivery::Output, DeliveryApplicationError> {
        self.telemetry.observe(OFFER_DELIVERY, classify, || {
            self.inner.offer_delivery(subject, input)
        })
    }

    fn list_available_delivery_offers(
        &self,
        subject: &Subject,
        input: list_available_delivery_offers::Input,
    ) -> Result<list_available_delivery_offers::Output, DeliveryApplicationError> {
        self.telemetry
            .observe(LIST_AVAILABLE_DELIVERY_OFFERS, classify, || {
                self.inner.list_available_delivery_offers(subject, input)
            })
    }

    fn get_courier_current_delivery(
        &self,
        subject: &Subject,
        input: get_courier_current_delivery::Input,
    ) -> Result<get_courier_current_delivery::Output, DeliveryApplicationError> {
        self.telemetry
            .observe(GET_COURIER_CURRENT_DELIVERY, classify, || {
                self.inner.get_courier_current_delivery(subject, input)
            })
    }

    fn accept_delivery_offer(
        &self,
        subject: &Subject,
        input: accept_delivery_offer::Input,
    ) -> Result<accept_delivery_offer::Output, DeliveryApplicationError> {
        self.telemetry.observe(ACCEPT_DELIVERY_OFFER, classify, || {
            self.inner.accept_delivery_offer(subject, input)
        })
    }

    fn reject_delivery_offer(
        &self,
        subject: &Subject,
        input: reject_delivery_offer::Input,
    ) -> Result<reject_delivery_offer::Output, DeliveryApplicationError> {
        self.telemetry.observe(REJECT_DELIVERY_OFFER, classify, || {
            self.inner.reject_delivery_offer(subject, input)
        })
    }

    fn reassign_courier(
        &self,
        subject: &Subject,
        input: reassign_courier::Input,
    ) -> Result<reassign_courier::Output, DeliveryApplicationError> {
        self.telemetry.observe(REASSIGN_COURIER, classify, || {
            self.inner.reassign_courier(subject, input)
        })
    }

    fn confirm_pickup(
        &self,
        subject: &Subject,
        input: confirm_pickup::Input,
    ) -> Result<confirm_pickup::Output, DeliveryApplicationError> {
        self.telemetry.observe(CONFIRM_PICKUP, classify, || {
            self.inner.confirm_pickup(subject, input)
        })
    }

    fn confirm_delivery(
        &self,
        subject: &Subject,
        input: confirm_delivery::Input,
    ) -> Result<confirm_delivery::Output, DeliveryApplicationError> {
        self.telemetry.observe(CONFIRM_DELIVERY, classify, || {
            self.inner.confirm_delivery(subject, input)
        })
    }

    fn report_delivery_failure(
        &self,
        subject: &Subject,
        input: report_delivery_failure::Input,
    ) -> Result<report_delivery_failure::Output, DeliveryApplicationError> {
        self.telemetry
            .observe(REPORT_DELIVERY_FAILURE, classify, || {
                self.inner.report_delivery_failure(subject, input)
            })
    }

    fn cancel_delivery(
        &self,
        subject: &Subject,
        input: cancel_delivery::Input,
    ) -> Result<cancel_delivery::Output, DeliveryApplicationError> {
        self.telemetry.observe(CANCEL_DELIVERY, classify, || {
            self.inner.cancel_delivery(subject, input)
        })
    }

    fn replan_delivery(
        &self,
        subject: &Subject,
        input: replan_delivery::Input,
    ) -> Result<replan_delivery::Output, DeliveryApplicationError> {
        self.telemetry.observe(REPLAN_DELIVERY, classify, || {
            self.inner.replan_delivery(subject, input)
        })
    }

    fn expire_delivery_offers(
        &self,
        input: expire_delivery_offers::Input,
    ) -> Result<expire_delivery_offers::Output, DeliveryApplicationError> {
        self.telemetry
            .observe(EXPIRE_DELIVERY_OFFERS, classify, || {
                self.inner.expire_delivery_offers(input)
            })
    }
}

#[cfg(test)]
#[path = "observed_use_cases/tests.rs"]
mod tests;

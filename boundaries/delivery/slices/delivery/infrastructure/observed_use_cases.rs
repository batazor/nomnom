//! Use case telemetry of the `delivery` slice (docs/observability.md#use-cases):
//! a decorator of [`DeliveryUseCases`] that the composition root puts in
//! front of the use cases, so transports and background jobs are observed
//! without changing the handlers. The slice owns the use case names and the
//! closed set of `error.type`:
//!
//! - `rejected`: `permission_denied`, `invalid_argument`, `not_found`,
//!   `failed_precondition`;
//! - `error`: `concurrent_modification`, `unavailable`, `transaction`,
//!   `internal`.
//!
//! A call refused with `InvalidTransition` is also the rejected FSM
//! transition of [`TransitionMetrics`].

use std::sync::Arc;

use crate::pkg::authz::Subject;
use crate::pkg::telemetry::usecase::{Failure, UseCase, UseCaseTelemetry};
use crate::slices::delivery::applications::error::DeliveryApplicationError;
use crate::slices::delivery::applications::{
    assignment::advance_assignment, assignment::create_delivery, assignment::find_courier,
    assignment::reassign_courier, duration::estimate_delivery_duration,
    duration::record_delivery_duration, execution::cancel_delivery, execution::confirm_delivery,
    execution::confirm_pickup, execution::get_courier_current_delivery,
    execution::report_delivery_failure, get_courier_pool, offer::accept_delivery_offer,
    offer::expire_delivery_offer, offer::list_available_delivery_offers, offer::offer_delivery,
    offer::offer_delivery_to_courier, offer::reject_delivery_offer, tracking::get_order_delivery,
};
use crate::slices::delivery::di::DeliveryUseCases;

use super::transitions::TransitionMetrics;

pub const CREATE_DELIVERY: UseCase = UseCase::command("create_delivery");
pub const FIND_COURIER: UseCase = UseCase::query("find_courier");
pub const OFFER_DELIVERY: UseCase = UseCase::command("offer_delivery");
pub const OFFER_DELIVERY_TO_COURIER: UseCase = UseCase::command("offer_delivery_to_courier");
pub const LIST_AVAILABLE_DELIVERY_OFFERS: UseCase =
    UseCase::query("list_available_delivery_offers");
pub const GET_COURIER_CURRENT_DELIVERY: UseCase = UseCase::query("get_courier_current_delivery");
pub const GET_COURIER_POOL: UseCase = UseCase::query("get_courier_pool");
pub const GET_ORDER_DELIVERY: UseCase = UseCase::query("get_order_delivery");
pub const ACCEPT_DELIVERY_OFFER: UseCase = UseCase::command("accept_delivery_offer");
pub const REJECT_DELIVERY_OFFER: UseCase = UseCase::command("reject_delivery_offer");
pub const REASSIGN_COURIER: UseCase = UseCase::command("reassign_courier");
pub const CONFIRM_PICKUP: UseCase = UseCase::command("confirm_pickup");
pub const CONFIRM_DELIVERY: UseCase = UseCase::command("confirm_delivery");
pub const REPORT_DELIVERY_FAILURE: UseCase = UseCase::command("report_delivery_failure");
pub const CANCEL_DELIVERY: UseCase = UseCase::command("cancel_delivery");
pub const ADVANCE_ASSIGNMENT: UseCase = UseCase::command("advance_assignment");
pub const EXPIRE_DELIVERY_OFFER: UseCase = UseCase::command("expire_delivery_offer");
pub const ESTIMATE_DELIVERY_DURATION: UseCase = UseCase::query("estimate_delivery_duration");
pub const RECORD_DELIVERY_DURATION: UseCase = UseCase::command("record_delivery_duration");

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
        DeliveryApplicationError::DeliveryForOrderInOtherMarket { .. } => {
            Failure::Rejected("already_exists")
        }
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

/// [`DeliveryUseCases`] observed by [`UseCaseTelemetry`] and
/// [`TransitionMetrics`]; results are returned unchanged.
pub struct ObservedDeliveryUseCases {
    inner: Arc<dyn DeliveryUseCases>,
    telemetry: Arc<UseCaseTelemetry>,
    transitions: TransitionMetrics,
}

impl ObservedDeliveryUseCases {
    #[must_use]
    pub fn new(
        inner: Arc<dyn DeliveryUseCases>,
        telemetry: Arc<UseCaseTelemetry>,
        transitions: TransitionMetrics,
    ) -> Self {
        Self {
            inner,
            telemetry,
            transitions,
        }
    }

    fn observe<T>(
        &self,
        use_case: UseCase,
        call: impl FnOnce() -> Result<T, DeliveryApplicationError>,
    ) -> Result<T, DeliveryApplicationError> {
        let result = self.telemetry.observe(use_case, classify, call);
        if let Err(error) = &result {
            self.transitions.rejected(error);
        }
        result
    }
}

impl DeliveryUseCases for ObservedDeliveryUseCases {
    fn create_delivery(
        &self,
        subject: &Subject,
        input: create_delivery::Input,
    ) -> Result<create_delivery::Output, DeliveryApplicationError> {
        self.observe(CREATE_DELIVERY, || {
            self.inner.create_delivery(subject, input)
        })
    }

    fn find_courier(
        &self,
        subject: &Subject,
        input: find_courier::Input,
    ) -> Result<find_courier::Output, DeliveryApplicationError> {
        self.observe(FIND_COURIER, || self.inner.find_courier(subject, input))
    }

    fn offer_delivery(
        &self,
        subject: &Subject,
        input: offer_delivery::Input,
    ) -> Result<offer_delivery::Output, DeliveryApplicationError> {
        self.observe(OFFER_DELIVERY, || self.inner.offer_delivery(subject, input))
    }

    fn offer_delivery_to_courier(
        &self,
        subject: &Subject,
        input: offer_delivery_to_courier::Input,
    ) -> Result<offer_delivery::Output, DeliveryApplicationError> {
        self.observe(OFFER_DELIVERY_TO_COURIER, || {
            self.inner.offer_delivery_to_courier(subject, input)
        })
    }

    fn list_available_delivery_offers(
        &self,
        subject: &Subject,
        input: list_available_delivery_offers::Input,
    ) -> Result<list_available_delivery_offers::Output, DeliveryApplicationError> {
        self.observe(LIST_AVAILABLE_DELIVERY_OFFERS, || {
            self.inner.list_available_delivery_offers(subject, input)
        })
    }

    fn get_courier_current_delivery(
        &self,
        subject: &Subject,
        input: get_courier_current_delivery::Input,
    ) -> Result<get_courier_current_delivery::Output, DeliveryApplicationError> {
        self.observe(GET_COURIER_CURRENT_DELIVERY, || {
            self.inner.get_courier_current_delivery(subject, input)
        })
    }

    fn get_courier_pool(
        &self,
        subject: &Subject,
        input: get_courier_pool::Input,
    ) -> Result<get_courier_pool::Output, DeliveryApplicationError> {
        self.observe(GET_COURIER_POOL, || {
            self.inner.get_courier_pool(subject, input)
        })
    }

    fn get_order_delivery(
        &self,
        subject: &Subject,
        input: get_order_delivery::Input,
    ) -> Result<get_order_delivery::Output, DeliveryApplicationError> {
        self.observe(GET_ORDER_DELIVERY, || {
            self.inner.get_order_delivery(subject, input)
        })
    }

    fn accept_delivery_offer(
        &self,
        subject: &Subject,
        input: accept_delivery_offer::Input,
    ) -> Result<accept_delivery_offer::Output, DeliveryApplicationError> {
        self.observe(ACCEPT_DELIVERY_OFFER, || {
            self.inner.accept_delivery_offer(subject, input)
        })
    }

    fn reject_delivery_offer(
        &self,
        subject: &Subject,
        input: reject_delivery_offer::Input,
    ) -> Result<reject_delivery_offer::Output, DeliveryApplicationError> {
        self.observe(REJECT_DELIVERY_OFFER, || {
            self.inner.reject_delivery_offer(subject, input)
        })
    }

    fn reassign_courier(
        &self,
        subject: &Subject,
        input: reassign_courier::Input,
    ) -> Result<reassign_courier::Output, DeliveryApplicationError> {
        self.observe(REASSIGN_COURIER, || {
            self.inner.reassign_courier(subject, input)
        })
    }

    fn confirm_pickup(
        &self,
        subject: &Subject,
        input: confirm_pickup::Input,
    ) -> Result<confirm_pickup::Output, DeliveryApplicationError> {
        self.observe(CONFIRM_PICKUP, || self.inner.confirm_pickup(subject, input))
    }

    fn confirm_delivery(
        &self,
        subject: &Subject,
        input: confirm_delivery::Input,
    ) -> Result<confirm_delivery::Output, DeliveryApplicationError> {
        self.observe(CONFIRM_DELIVERY, || {
            self.inner.confirm_delivery(subject, input)
        })
    }

    fn report_delivery_failure(
        &self,
        subject: &Subject,
        input: report_delivery_failure::Input,
    ) -> Result<report_delivery_failure::Output, DeliveryApplicationError> {
        self.observe(REPORT_DELIVERY_FAILURE, || {
            self.inner.report_delivery_failure(subject, input)
        })
    }

    fn cancel_delivery(
        &self,
        subject: &Subject,
        input: cancel_delivery::Input,
    ) -> Result<cancel_delivery::Output, DeliveryApplicationError> {
        self.observe(CANCEL_DELIVERY, || {
            self.inner.cancel_delivery(subject, input)
        })
    }

    fn advance_assignment(
        &self,
        input: advance_assignment::Input,
    ) -> Result<advance_assignment::Output, DeliveryApplicationError> {
        self.observe(ADVANCE_ASSIGNMENT, || self.inner.advance_assignment(input))
    }

    fn expire_delivery_offer(
        &self,
        input: expire_delivery_offer::Input,
    ) -> Result<expire_delivery_offer::Output, DeliveryApplicationError> {
        self.observe(EXPIRE_DELIVERY_OFFER, || {
            self.inner.expire_delivery_offer(input)
        })
    }

    fn estimate_delivery_duration(
        &self,
        subject: &Subject,
        input: estimate_delivery_duration::Input,
    ) -> Result<estimate_delivery_duration::Output, DeliveryApplicationError> {
        self.observe(ESTIMATE_DELIVERY_DURATION, || {
            self.inner.estimate_delivery_duration(subject, input)
        })
    }

    fn record_delivery_duration(
        &self,
        input: record_delivery_duration::Input,
    ) -> Result<record_delivery_duration::Output, DeliveryApplicationError> {
        self.observe(RECORD_DELIVERY_DURATION, || {
            self.inner.record_delivery_duration(input)
        })
    }
}

#[cfg(test)]
#[path = "observed_use_cases/tests.rs"]
mod tests;

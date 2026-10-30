//! Use case telemetry of the `courier` slice (docs/observability.md#use-cases):
//! a decorator of [`CourierUseCases`] put in front of the use cases by the
//! composition root. Closed set of `error.type`:
//!
//! - `rejected`: `permission_denied`, `invalid_argument`, `not_found`,
//!   `already_exists`, `failed_precondition`;
//! - `error`: `concurrent_modification`, `unavailable`, `internal`.

use std::sync::Arc;

use crate::pkg::authz::Subject;
use crate::pkg::telemetry::usecase::{Failure, UseCase, UseCaseTelemetry};
use crate::slices::courier::applications::error::CourierApplicationError;
use crate::slices::courier::applications::{
    register_courier, retire_courier, set_courier_availability, update_courier_location,
};
use crate::slices::courier::di::CourierUseCases;

pub const REGISTER_COURIER: UseCase = UseCase::command("register_courier");
pub const SET_COURIER_AVAILABILITY: UseCase = UseCase::command("set_courier_availability");
pub const RETIRE_COURIER: UseCase = UseCase::command("retire_courier");
pub const UPDATE_COURIER_LOCATION: UseCase = UseCase::command("update_courier_location");

/// `nomnom.outcome` and `error.type` of a failed call.
#[must_use]
pub fn classify(error: &CourierApplicationError) -> Failure {
    match error {
        CourierApplicationError::PermissionDenied => Failure::Rejected("permission_denied"),
        CourierApplicationError::InvalidRequest(_) => Failure::Rejected("invalid_argument"),
        CourierApplicationError::CourierNotFound(_) => Failure::Rejected("not_found"),
        CourierApplicationError::CourierAlreadyRegistered(_) => Failure::Rejected("already_exists"),
        CourierApplicationError::PreconditionFailed(_) => Failure::Rejected("failed_precondition"),
        CourierApplicationError::ConcurrentModification => {
            Failure::Error("concurrent_modification")
        }
        CourierApplicationError::DependencyUnavailable(_) => Failure::Error("unavailable"),
        CourierApplicationError::Internal(_) => Failure::Error("internal"),
    }
}

/// [`CourierUseCases`] observed by [`UseCaseTelemetry`]; results are
/// returned unchanged.
pub struct ObservedCourierUseCases {
    inner: Arc<dyn CourierUseCases>,
    telemetry: Arc<UseCaseTelemetry>,
}

impl ObservedCourierUseCases {
    #[must_use]
    pub fn new(inner: Arc<dyn CourierUseCases>, telemetry: Arc<UseCaseTelemetry>) -> Self {
        Self { inner, telemetry }
    }
}

impl CourierUseCases for ObservedCourierUseCases {
    fn register_courier(
        &self,
        subject: &Subject,
        input: register_courier::Input,
    ) -> Result<register_courier::Output, CourierApplicationError> {
        self.telemetry.observe(REGISTER_COURIER, classify, || {
            self.inner.register_courier(subject, input)
        })
    }

    fn set_courier_availability(
        &self,
        subject: &Subject,
        input: set_courier_availability::Input,
    ) -> Result<set_courier_availability::Output, CourierApplicationError> {
        self.telemetry
            .observe(SET_COURIER_AVAILABILITY, classify, || {
                self.inner.set_courier_availability(subject, input)
            })
    }

    fn retire_courier(
        &self,
        subject: &Subject,
        input: retire_courier::Input,
    ) -> Result<retire_courier::Output, CourierApplicationError> {
        self.telemetry.observe(RETIRE_COURIER, classify, || {
            self.inner.retire_courier(subject, input)
        })
    }

    fn update_courier_location(
        &self,
        subject: &Subject,
        input: update_courier_location::Input,
    ) -> Result<update_courier_location::Output, CourierApplicationError> {
        self.telemetry
            .observe(UPDATE_COURIER_LOCATION, classify, || {
                self.inner.update_courier_location(subject, input)
            })
    }
}

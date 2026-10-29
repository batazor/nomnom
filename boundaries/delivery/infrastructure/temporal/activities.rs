//! Activities of the Assignment Workflow over the use cases Advance
//! Assignment and Expire Delivery Offer. Use cases and the store are
//! synchronous, so every call runs on a blocking thread.
//!
//! Failures: an unknown Delivery or offer cannot succeed on retry and fails
//! the workflow; everything else — a concurrent change, a courier that
//! stopped being available, an unavailable dependency — is retried by the
//! retry policy of the workflow.

use std::sync::Arc;

use serde::{Deserialize, Serialize};
use temporalio_common::error::ApplicationFailure;
use temporalio_macros::activities;
use temporalio_sdk::activities::{ActivityContext, ActivityError};

use crate::slices::delivery::applications::advance_assignment;
use crate::slices::delivery::applications::error::DeliveryApplicationError;

/// What the workflow waits for after a step: [`advance_assignment::Output`]
/// as the Activity payload.
#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
pub enum Step {
    Offered {
        offer_id: String,
        expires_at_epoch_seconds: i64,
    },
    NoCandidate,
    Assigned,
    Finished,
}

impl From<advance_assignment::Output> for Step {
    fn from(output: advance_assignment::Output) -> Self {
        match output {
            advance_assignment::Output::Offered {
                offer_id,
                expires_at_epoch_seconds,
            } => Self::Offered {
                offer_id: offer_id.as_str().to_owned(),
                expires_at_epoch_seconds,
            },
            advance_assignment::Output::NoCandidate => Self::NoCandidate,
            advance_assignment::Output::Assigned => Self::Assigned,
            advance_assignment::Output::Finished => Self::Finished,
        }
    }
}

/// The use cases the Activities call; the worker composition wires them.
pub trait AssignmentSteps: Send + Sync {
    fn advance(&self, delivery_id: String) -> Result<Step, DeliveryApplicationError>;
    fn expire(&self, offer_id: String) -> Result<(), DeliveryApplicationError>;
}

pub struct AssignmentActivities {
    steps: Arc<dyn AssignmentSteps>,
}

impl AssignmentActivities {
    #[must_use]
    pub fn new(steps: Arc<dyn AssignmentSteps>) -> Self {
        Self { steps }
    }
}

#[activities]
impl AssignmentActivities {
    #[activity]
    pub async fn advance(
        self: Arc<Self>,
        _ctx: ActivityContext,
        delivery_id: String,
    ) -> Result<Step, ActivityError> {
        let steps = Arc::clone(&self.steps);
        blocking(move || steps.advance(delivery_id)).await
    }

    #[activity]
    pub async fn expire(
        self: Arc<Self>,
        _ctx: ActivityContext,
        offer_id: String,
    ) -> Result<(), ActivityError> {
        let steps = Arc::clone(&self.steps);
        blocking(move || steps.expire(offer_id)).await
    }
}

async fn blocking<T: Send + 'static>(
    call: impl FnOnce() -> Result<T, DeliveryApplicationError> + Send + 'static,
) -> Result<T, ActivityError> {
    tokio::task::spawn_blocking(call)
        .await
        .map_err(|error| ActivityError::application(ApplicationFailure::new(error)))?
        .map_err(failure)
}

fn failure(error: DeliveryApplicationError) -> ActivityError {
    let message = error.to_string();
    ActivityError::application(match error {
        DeliveryApplicationError::DeliveryNotFound(_)
        | DeliveryApplicationError::DeliveryOfferNotFound(_)
        | DeliveryApplicationError::InvalidRequest(_) => ApplicationFailure::non_retryable(message),
        _ => ApplicationFailure::new(message),
    })
}

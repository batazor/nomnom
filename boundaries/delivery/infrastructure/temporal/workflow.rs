//! The Assignment Workflow of one Delivery.
//!
//! Loop: Advance Assignment says what to wait for. An open offer waits for a
//! wake or a timer one second past its deadline (the deadline itself is
//! inclusive); the timer runs Expire Delivery Offer. Without a candidate or
//! with an assigned courier it waits for a wake. Pickup and the closing of
//! the Delivery finish the workflow. The count of wakes is read before the
//! step, so a wake that arrives while the step runs is not missed.

use std::time::{Duration, SystemTime, UNIX_EPOCH};

use temporalio_common::RetryPolicy;
use temporalio_macros::{workflow, workflow_methods};
use temporalio_sdk::{
    ActivityOptions, ContinueAsNewOptions, SyncWorkflowContext, WorkflowContext, WorkflowResult,
};

use super::activities::{AssignmentActivities, Step};

const ACTIVITY_START_TO_CLOSE: Duration = Duration::from_secs(30);

#[workflow]
#[derive(Default)]
pub struct AssignmentWorkflow {
    wakes: u64,
}

#[workflow_methods]
impl AssignmentWorkflow {
    #[run]
    pub async fn run(ctx: &mut WorkflowContext<Self>, delivery_id: String) -> WorkflowResult<()> {
        loop {
            if ctx.continue_as_new_suggested() {
                ctx.continue_as_new(delivery_id.clone(), ContinueAsNewOptions::default())?;
            }
            let seen = ctx.state(|workflow| workflow.wakes);
            let step = ctx
                .execute_activity(
                    AssignmentActivities::advance,
                    delivery_id.clone(),
                    activity_options(),
                )
                .await?;
            match step {
                Step::Finished => return Ok(()),
                Step::Offered {
                    offer_id,
                    expires_at_epoch_seconds,
                } => {
                    let remaining = until(ctx.workflow_time(), expires_at_epoch_seconds + 1);
                    temporalio_sdk::workflows::select! {
                        _ = ctx.timer(remaining) => {
                            ctx.execute_activity(
                                AssignmentActivities::expire,
                                offer_id,
                                activity_options(),
                            )
                            .await?;
                        }
                        woken = ctx.wait_condition(|workflow: &Self| workflow.wakes != seen) => {
                            woken?;
                        }
                    }
                }
                Step::NoCandidate | Step::Assigned => {
                    ctx.wait_condition(|workflow| workflow.wakes != seen)
                        .await?;
                }
            }
        }
    }

    /// Wake with its reason; the reason is only for the Event History.
    #[signal]
    pub fn wake(&mut self, _ctx: &mut SyncWorkflowContext<Self>, _reason: String) {
        self.wakes += 1;
    }
}

fn activity_options() -> ActivityOptions {
    ActivityOptions::with_start_to_close_timeout(ACTIVITY_START_TO_CLOSE)
        .retry_policy(
            RetryPolicy::builder()
                .initial_interval(Duration::from_secs(1))
                .backoff_coefficient(2.0)
                .maximum_interval(Duration::from_secs(60))
                .build(),
        )
        .build()
}

/// Time from the workflow clock to `epoch_seconds`; zero when it has passed
/// or the clock is not known yet.
fn until(now: Option<SystemTime>, epoch_seconds: i64) -> Duration {
    let deadline = UNIX_EPOCH + Duration::from_secs(u64::try_from(epoch_seconds).unwrap_or(0));
    now.and_then(|now| deadline.duration_since(now).ok())
        .unwrap_or(Duration::ZERO)
}

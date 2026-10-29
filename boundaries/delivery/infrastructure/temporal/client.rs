//! Temporal client of the use cases: starts and wakes the Assignment
//! Workflow after their commit (FR-707) and wakes the waiting Delivery a
//! Courier Candidate may take (FR-706).
//!
//! The ports are synchronous, as the store is: the client owns a small tokio
//! runtime and every call runs `block_on` on it through
//! [`blocking`](crate::infrastructure::repository::blocking).
//!
//! - start: `RejectDuplicate` and `UseExisting`, so a running workflow is
//!   kept and a finished one is not started again; both are success;
//! - wake: a workflow that is already finished is not an error (signal
//!   `NotFound`);
//! - any other failure is [`AssignmentProcessError`]: the use case already
//!   committed and answers `UNAVAILABLE`, the caller repeats it.

use std::sync::Arc;

use temporalio_client::errors::{WorkflowInteractionError, WorkflowStartError};
use temporalio_client::{
    Client, ClientOptions, Connection, ConnectionOptions, WorkflowIdConflictPolicy,
    WorkflowIdReusePolicy, WorkflowSignalOptions, WorkflowStartOptions,
};
use tokio::runtime::Runtime;
use url::Url;

use crate::infrastructure::repository::blocking;
use crate::pkg::config::TemporalConfig;
use crate::slices::courier::applications::candidate_wake::{CandidateWake, CandidateWakeError};
use crate::slices::courier::domains::courier::CourierId;
use crate::slices::delivery::applications::assignment_process::{
    AssignmentProcess, AssignmentProcessError, CandidateDeliveries, WakeReason,
};
use crate::slices::delivery::domains::delivery::DeliveryId;

use super::workflow::AssignmentWorkflow;
use super::{TASK_QUEUE, workflow_id};

/// Query of the Delivery a Courier Candidate wakes; the PostgreSQL store.
pub trait WakeTargets: CandidateDeliveries + Send + Sync {}

impl<T: CandidateDeliveries + Send + Sync> WakeTargets for T {}

pub struct TemporalAssignment {
    client: Client,
    candidates: Arc<dyn WakeTargets>,
    runtime: Option<Runtime>,
}

impl TemporalAssignment {
    /// Connects to the frontend; an unreachable server fails startup.
    pub fn connect(
        config: &TemporalConfig,
        candidates: Arc<dyn WakeTargets>,
    ) -> Result<Self, String> {
        let target = Url::parse(&format!("http://{}", config.host_port))
            .map_err(|error| format!("temporal host_port {}: {error}", config.host_port))?;
        let runtime = tokio::runtime::Builder::new_multi_thread()
            .worker_threads(1)
            .thread_name("delivery-temporal")
            .enable_all()
            .build()
            .map_err(|error| format!("temporal client runtime: {error}"))?;
        let options = ConnectionOptions::new(target)
            .identity(format!("delivery@{}", std::process::id()))
            .connect_timeout(config.connect_timeout)
            .build();
        let namespace = config.namespace.clone();
        let connected = blocking(|| {
            runtime.block_on(async move {
                let connection = Connection::connect(options)
                    .await
                    .map_err(|error| format!("connect temporal: {error}"))?;
                Client::new(connection, ClientOptions::new(namespace).build())
                    .map_err(|error| format!("temporal client: {error}"))
            })
        });
        match connected {
            Ok(client) => Ok(Self {
                client,
                candidates,
                runtime: Some(runtime),
            }),
            Err(error) => {
                runtime.shutdown_background();
                Err(error)
            }
        }
    }

    fn runtime(&self) -> &Runtime {
        self.runtime.as_ref().expect("runtime lives until drop")
    }

    fn signal(&self, delivery_id: &DeliveryId, reason: WakeReason) -> Result<(), String> {
        let handle = self
            .client
            .get_workflow_handle::<AssignmentWorkflow>(workflow_id(delivery_id.as_str()));
        let signalled = blocking(|| {
            self.runtime().block_on(handle.signal(
                AssignmentWorkflow::wake,
                reason.as_str().to_owned(),
                WorkflowSignalOptions::default(),
            ))
        });
        match signalled {
            Ok(()) | Err(WorkflowInteractionError::NotFound(_)) => Ok(()),
            Err(error) => Err(format!(
                "wake {}: {error}",
                workflow_id(delivery_id.as_str())
            )),
        }
    }
}

impl AssignmentProcess for TemporalAssignment {
    fn start(&self, delivery_id: &DeliveryId) -> Result<(), AssignmentProcessError> {
        let id = workflow_id(delivery_id.as_str());
        let options = WorkflowStartOptions::new(TASK_QUEUE, id.clone())
            .id_reuse_policy(WorkflowIdReusePolicy::RejectDuplicate)
            .id_conflict_policy(WorkflowIdConflictPolicy::UseExisting)
            .build();
        let started = blocking(|| {
            self.runtime().block_on(self.client.start_workflow(
                AssignmentWorkflow::run,
                delivery_id.as_str().to_owned(),
                options,
            ))
        });
        match started {
            Ok(_) | Err(WorkflowStartError::AlreadyStarted { .. }) => Ok(()),
            Err(error) => Err(AssignmentProcessError(format!("start {id}: {error}"))),
        }
    }

    fn wake(
        &self,
        delivery_id: &DeliveryId,
        reason: WakeReason,
    ) -> Result<(), AssignmentProcessError> {
        self.signal(delivery_id, reason)
            .map_err(AssignmentProcessError)
    }
}

impl CandidateWake for TemporalAssignment {
    fn courier_available(&self, courier_id: &CourierId) -> Result<(), CandidateWakeError> {
        let target = self
            .candidates
            .oldest_waiting_for(courier_id)
            .map_err(|error| CandidateWakeError(error.to_string()))?;
        match target {
            Some(delivery_id) => self
                .signal(&delivery_id, WakeReason::CourierAvailable)
                .map_err(CandidateWakeError),
            None => Ok(()),
        }
    }
}

impl Drop for TemporalAssignment {
    fn drop(&mut self) {
        if let Some(runtime) = self.runtime.take() {
            // Dropping a runtime blocks; shutdown_background works in an async
            // context too.
            runtime.shutdown_background();
        }
    }
}

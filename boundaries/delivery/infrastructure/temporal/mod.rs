//! Temporal adapter of the Assignment Workflow (FR-704…FR-708): the workflow
//! `delivery:<delivery_id>` on the task queue `delivery`, its Activities over
//! the use cases Advance Assignment and Expire Delivery Offer, and the client
//! that starts and wakes it after the commit of a use case.
//!
//! The workflow keeps only the count of wakes: every step reads the stored
//! Delivery, so PostgreSQL stays the source of truth and a lost or extra wake
//! changes nothing but the time of the next read. Activity timeouts and the
//! retry policy are constants of the workflow code, as in `orders`.

mod activities;
mod client;
mod workflow;

pub use activities::{AssignmentActivities, AssignmentSteps, Step};
pub use client::TemporalAssignment;
pub use workflow::AssignmentWorkflow;

/// Task queue of the Assignment Workflow and its Activities.
pub const TASK_QUEUE: &str = "delivery";

/// Workflow ID of the Assignment Workflow of a Delivery.
#[must_use]
pub fn workflow_id(delivery_id: &str) -> String {
    format!("delivery:{delivery_id}")
}

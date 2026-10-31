//! Infrastructure of the `delivery` slice: the Domain Event → Integration Event
//! mapper of the Outbox (ADR 31), the business metrics decorator of the Unit
//! of Work, Outbox and repository ports, which also logs and traces committed
//! Delivery Failures and counts FSM transitions, the use case telemetry and
//! the consumer of the Delivery's own facts for the Delivery Duration
//! Statistics.

mod duration_facts;
pub mod failure_log;
mod integration_events;
pub mod observability;
pub mod observed_use_cases;
pub mod random;
pub mod transitions;

pub use duration_facts::{duration_input, handling_error};
pub use integration_events::{
    COURIER_ASSIGNED_TO_DELIVERY, DELIVERY_COMPLETED, DELIVERY_FAILED, DELIVERY_PICKED_UP,
    DeliveryIntegrationEvents,
};

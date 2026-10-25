//! Infrastructure of the `delivery` slice: the Domain Event → Integration Event
//! mapper of the Outbox (ADR 31) and the business metrics decorator of the Unit
//! of Work and Outbox ports.

mod integration_events;
pub mod observability;

pub use integration_events::{
    COURIER_ASSIGNED_TO_DELIVERY, DELIVERY_COMPLETED, DELIVERY_FAILED, DELIVERY_PICKED_UP,
    DeliveryIntegrationEvents,
};

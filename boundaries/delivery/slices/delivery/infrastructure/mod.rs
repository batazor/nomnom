//! Infrastructure of the `delivery` slice: the Domain Event → Integration Event
//! mapper of the Outbox (ADR 31).

mod integration_events;

pub use integration_events::{
    COURIER_ASSIGNED_TO_DELIVERY, DELIVERY_COMPLETED, DELIVERY_FAILED, DELIVERY_PICKED_UP,
    DeliveryIntegrationEvents,
};

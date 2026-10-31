//! `DeliveryOfferAcceptable`: whether a courier may accept an offer at the
//! moment of acceptance.

use super::machine::past_deadline;

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum DeliveryOfferReason {
    Expired,
    TooFarFromPickup,
    ActiveAssignmentLimitReached,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct DeliveryOfferResult {
    pub reasons: Vec<DeliveryOfferReason>,
}

impl DeliveryOfferResult {
    #[must_use]
    pub fn satisfied(&self) -> bool {
        self.reasons.is_empty()
    }
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct DeliveryOfferContext {
    pub now_epoch_seconds: i64,
    pub expires_at_epoch_seconds: i64,
    pub distance_to_pickup_meters: u32,
    pub maximum_distance_meters: u32,
    pub active_assignments: u32,
    pub maximum_active_assignments: u32,
}

#[must_use]
pub fn delivery_offer_acceptable(context: DeliveryOfferContext) -> DeliveryOfferResult {
    let mut reasons = Vec::new();
    if past_deadline(context.now_epoch_seconds, context.expires_at_epoch_seconds) {
        reasons.push(DeliveryOfferReason::Expired);
    }
    if context.distance_to_pickup_meters > context.maximum_distance_meters {
        reasons.push(DeliveryOfferReason::TooFarFromPickup);
    }
    if context.active_assignments >= context.maximum_active_assignments {
        reasons.push(DeliveryOfferReason::ActiveAssignmentLimitReached);
    }
    DeliveryOfferResult { reasons }
}

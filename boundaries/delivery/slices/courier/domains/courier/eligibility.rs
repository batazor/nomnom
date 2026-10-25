#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum CourierEligibilityReason {
    OutsideDeliveryZone,
    TooFarFromPickup,
    ActiveAssignmentLimitReached,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct CourierEligibilityResult {
    pub reasons: Vec<CourierEligibilityReason>,
}

impl CourierEligibilityResult {
    #[must_use]
    pub fn satisfied(&self) -> bool {
        self.reasons.is_empty()
    }
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct CourierEligibilityContext {
    pub within_delivery_zone: bool,
    pub distance_to_pickup_meters: u32,
    pub maximum_distance_meters: u32,
    pub active_assignments: u32,
    pub maximum_active_assignments: u32,
}

#[must_use]
pub fn courier_eligible_for_delivery(
    context: CourierEligibilityContext,
) -> CourierEligibilityResult {
    let mut reasons = Vec::new();
    if !context.within_delivery_zone {
        reasons.push(CourierEligibilityReason::OutsideDeliveryZone);
    }
    if context.distance_to_pickup_meters > context.maximum_distance_meters {
        reasons.push(CourierEligibilityReason::TooFarFromPickup);
    }
    if context.active_assignments >= context.maximum_active_assignments {
        reasons.push(CourierEligibilityReason::ActiveAssignmentLimitReached);
    }
    CourierEligibilityResult { reasons }
}

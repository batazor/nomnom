use delivery::slices::courier::domains::courier::{
    CourierEligibilityContext, CourierEligibilityReason, MarketId, courier_eligible_for_delivery,
};
use delivery::slices::delivery::domains::delivery::{
    DeliveryOfferContext, DeliveryOfferReason, delivery_offer_acceptable,
};

fn market(value: &str) -> MarketId {
    MarketId::new(value).expect("market id")
}

#[test]
fn courier_is_eligible_on_distance_boundary_with_spare_assignment_capacity() {
    let berlin = market("berlin");
    let result = courier_eligible_for_delivery(CourierEligibilityContext {
        delivery_market_id: &berlin,
        courier_market_id: &berlin,
        within_delivery_zone: true,
        distance_to_pickup_meters: 5_000,
        maximum_distance_meters: 5_000,
        active_assignments: 0,
        maximum_active_assignments: 1,
    });

    assert!(result.satisfied());
    assert!(result.reasons.is_empty());
}

#[test]
fn courier_eligibility_collects_market_zone_distance_and_assignment_reasons() {
    let (berlin, hamburg) = (market("berlin"), market("hamburg"));
    let result = courier_eligible_for_delivery(CourierEligibilityContext {
        delivery_market_id: &berlin,
        courier_market_id: &hamburg,
        within_delivery_zone: false,
        distance_to_pickup_meters: 5_001,
        maximum_distance_meters: 5_000,
        active_assignments: 1,
        maximum_active_assignments: 1,
    });

    assert_eq!(
        result.reasons,
        vec![
            CourierEligibilityReason::OtherMarket,
            CourierEligibilityReason::OutsideDeliveryZone,
            CourierEligibilityReason::TooFarFromPickup,
            CourierEligibilityReason::ActiveAssignmentLimitReached,
        ]
    );
}

/// ADR 41: a courier of another Market is not eligible even when every
/// other condition holds.
#[test]
fn courier_of_another_market_is_not_eligible() {
    let (berlin, hamburg) = (market("berlin"), market("hamburg"));
    let result = courier_eligible_for_delivery(CourierEligibilityContext {
        delivery_market_id: &berlin,
        courier_market_id: &hamburg,
        within_delivery_zone: true,
        distance_to_pickup_meters: 0,
        maximum_distance_meters: 5_000,
        active_assignments: 0,
        maximum_active_assignments: 1,
    });

    assert!(!result.satisfied());
    assert_eq!(result.reasons, vec![CourierEligibilityReason::OtherMarket]);
}

#[test]
fn delivery_offer_is_acceptable_on_expiry_and_distance_boundaries() {
    let result = delivery_offer_acceptable(DeliveryOfferContext {
        now_epoch_seconds: 1_000,
        expires_at_epoch_seconds: 1_000,
        distance_to_pickup_meters: 5_000,
        maximum_distance_meters: 5_000,
        active_assignments: 0,
        maximum_active_assignments: 1,
    });

    assert!(result.satisfied());
}

#[test]
fn delivery_offer_collects_expiry_distance_and_assignment_reasons() {
    let result = delivery_offer_acceptable(DeliveryOfferContext {
        now_epoch_seconds: 1_001,
        expires_at_epoch_seconds: 1_000,
        distance_to_pickup_meters: 5_001,
        maximum_distance_meters: 5_000,
        active_assignments: 1,
        maximum_active_assignments: 1,
    });

    assert_eq!(
        result.reasons,
        vec![
            DeliveryOfferReason::Expired,
            DeliveryOfferReason::TooFarFromPickup,
            DeliveryOfferReason::ActiveAssignmentLimitReached,
        ]
    );
}

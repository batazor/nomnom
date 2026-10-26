use super::*;
use crate::infrastructure::memory::StaticAuthorization;
use crate::slices::courier::domains::courier::CourierStatus;
use crate::slices::delivery::applications::test_support::{World, candidate, hamburg, service};
use crate::slices::delivery::domains::delivery::{DeliveryError, State, TransitionError};

fn handler(world: &World) -> Handler<'_> {
    Handler {
        authorization: &world.authorization,
        deliveries: &world.store,
        couriers: &world.store,
        offers: &world.store,
        offers_of: &world.store,
        locator: &world.locator,
        policy: world.policy,
    }
}

fn input() -> Input {
    Input {
        delivery_id: "delivery-1".to_owned(),
    }
}

/// AC-317
#[test]
fn selects_nearest_available_eligible_courier() {
    let world = World::new();
    world.waiting_delivery("delivery-1");
    world.courier("far", CourierStatus::Available);
    world.courier("near", CourierStatus::Available);
    world.courier("tie", CourierStatus::Available);
    world.locator.set_candidates(vec![
        candidate("far", 4_000),
        candidate("tie", 1_000),
        candidate("near", 1_000),
    ]);

    let output = handler(&world)
        .execute(&service(), input())
        .expect("search");

    assert_eq!(
        output.outcome,
        Outcome::CandidateFound {
            courier_id: CourierId::new("near"),
            distance_to_pickup_meters: 1_000,
        }
    );
    assert!(output.rejections.is_empty());
    assert_eq!(world.delivery_state("delivery-1"), State::Waiting);
    assert_eq!(world.courier_status("near"), CourierStatus::Available);
    assert_eq!(world.store.commits(), 0);
}

/// AC-318
#[test]
fn reports_no_candidate_with_reason_per_courier() {
    let world = World::new();
    world.waiting_delivery("delivery-1");
    world.courier("busy", CourierStatus::Busy);
    world.courier("off", CourierStatus::Unavailable);
    world.courier("outside", CourierStatus::Available);
    world.courier("loaded", CourierStatus::Available);
    world.courier("offered", CourierStatus::Available);
    world.offer("offer-1", "delivery-1", "offered");
    let mut outside = candidate("outside", 6_000);
    outside.within_delivery_zone = false;
    let mut loaded = candidate("loaded", 100);
    loaded.active_assignments = 1;
    world.locator.set_candidates(vec![
        candidate("busy", 100),
        candidate("off", 100),
        outside,
        loaded,
        candidate("offered", 100),
        candidate("ghost", 100),
    ]);

    let output = handler(&world)
        .execute(&service(), input())
        .expect("search");

    assert_eq!(output.outcome, Outcome::NoCandidate);
    let rejection = |courier: &str, reason| CandidateRejection {
        courier_id: CourierId::new(courier),
        reason,
    };
    assert_eq!(
        output.rejections,
        vec![
            rejection(
                "busy",
                CandidateRejectionReason::NotAvailable(CourierError::AlreadyBusy)
            ),
            rejection(
                "off",
                CandidateRejectionReason::NotAvailable(CourierError::Unavailable)
            ),
            rejection(
                "outside",
                CandidateRejectionReason::Ineligible(vec![
                    CourierEligibilityReason::OutsideDeliveryZone,
                    CourierEligibilityReason::TooFarFromPickup,
                ])
            ),
            rejection(
                "loaded",
                CandidateRejectionReason::Ineligible(vec![
                    CourierEligibilityReason::ActiveAssignmentLimitReached
                ])
            ),
            rejection("offered", CandidateRejectionReason::AlreadyOffered),
            rejection("ghost", CandidateRejectionReason::CourierNotRegistered),
        ]
    );
}

/// ADR 41: a courier of another Market than the Delivery is never selected,
/// however near: the `CourierEligibleForDelivery` specification rejects the
/// candidate, and the courier port rejects a registered courier of another
/// Market even when the locator reports it with the Delivery's Market.
#[test]
fn courier_of_another_market_is_never_selected() {
    let world = World::new();
    world.waiting_delivery("delivery-1");
    world.courier("berlin-far", CourierStatus::Available);
    world.courier_in("hamburg-near", CourierStatus::Available, &hamburg());
    world.courier_in("hamburg-mislabelled", CourierStatus::Available, &hamburg());
    let mut near = candidate("hamburg-near", 10);
    near.market_id = hamburg();
    world.locator.set_candidates(vec![
        near,
        candidate("hamburg-mislabelled", 10),
        candidate("berlin-far", 4_000),
    ]);

    let output = handler(&world)
        .execute(&service(), input())
        .expect("search");

    assert_eq!(
        output.outcome,
        Outcome::CandidateFound {
            courier_id: CourierId::new("berlin-far"),
            distance_to_pickup_meters: 4_000,
        }
    );
    assert_eq!(
        output.rejections,
        vec![
            CandidateRejection {
                courier_id: CourierId::new("hamburg-near"),
                reason: CandidateRejectionReason::Ineligible(vec![
                    CourierEligibilityReason::OtherMarket
                ]),
            },
            CandidateRejection {
                courier_id: CourierId::new("hamburg-mislabelled"),
                reason: CandidateRejectionReason::NotAvailable(CourierError::OtherMarket),
            },
        ]
    );
}

/// AC-319
#[test]
fn assigned_delivery_is_not_searched() {
    let world = World::new();
    world.assigned_delivery("delivery-1", "courier-1");

    assert_eq!(
        handler(&world).execute(&service(), input()),
        Err(DeliveryApplicationError::DeliveryPrecondition(
            DeliveryError::Transition(TransitionError::AlreadyInState {
                state: State::Assigned
            })
        ))
    );
}

#[test]
fn unknown_delivery_is_not_found() {
    let world = World::new();
    assert_eq!(
        handler(&world).execute(&service(), input()),
        Err(DeliveryApplicationError::DeliveryNotFound(DeliveryId::new(
            "delivery-1"
        )))
    );
}

#[test]
fn locator_outage_is_dependency_unavailable() {
    let world = World::new();
    world.waiting_delivery("delivery-1");
    world.locator.set_unavailable(true);
    assert!(matches!(
        handler(&world).execute(&service(), input()),
        Err(DeliveryApplicationError::DependencyUnavailable(_))
    ));
}

#[test]
fn denied_subject_cannot_search() {
    let world = World::with_authorization(StaticAuthorization::deny_all());
    world.waiting_delivery("delivery-1");
    assert_eq!(
        handler(&world).execute(&service(), input()),
        Err(DeliveryApplicationError::PermissionDenied)
    );
}

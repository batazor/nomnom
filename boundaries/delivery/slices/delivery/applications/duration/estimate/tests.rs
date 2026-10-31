use super::*;
use crate::infrastructure::memory::StaticAuthorization;
use crate::slices::courier::domains::courier::MarketIdError;
use crate::slices::delivery::applications::duration::record::DeliveryDurationStatistics;
use crate::slices::delivery::applications::error::InvalidDeliveryRequest;
use crate::slices::delivery::applications::test_support::{World, berlin, hamburg, service};
use crate::slices::delivery::domains::delivery::DeliveryId;

fn minutes(value: u64) -> Duration {
    Duration::from_secs(value * 60)
}

fn policy() -> DeliveryEstimatePolicy {
    DeliveryEstimatePolicy::new(3, minutes(15), minutes(20)).expect("policy")
}

fn handler(world: &World) -> Handler<'_> {
    Handler {
        authorization: &world.authorization,
        samples: &world.store,
        policy: policy(),
    }
}

fn input(market: &str) -> Input {
    Input {
        market_id: market.to_owned(),
    }
}

fn sample(world: &World, market_id: &MarketId, kind: DurationKind, durations: &[u64]) {
    for (index, duration) in durations.iter().enumerate() {
        world
            .store
            .record_duration(
                market_id,
                kind,
                &DeliveryId::new(format!("{}-{}-{index}", market_id.as_str(), kind.as_str())),
                minutes(*duration),
                policy().sample_size(),
            )
            .expect("seed sample");
    }
}

/// AC-904
#[test]
fn full_sample_gives_the_median_of_the_market() {
    let world = World::new();
    sample(&world, &berlin(), DurationKind::Transit, &[30, 22, 18]);
    sample(&world, &berlin(), DurationKind::CourierLead, &[9, 7, 12]);
    sample(&world, &hamburg(), DurationKind::Transit, &[60, 60, 60]);

    assert_eq!(
        handler(&world).execute(&service(), input("berlin")),
        Ok(DeliveryDurationEstimate {
            courier_lead: minutes(9),
            transit: minutes(22),
        })
    );
}

/// AC-905
#[test]
fn incomplete_sample_gives_the_baselines() {
    let world = World::new();
    sample(&world, &berlin(), DurationKind::Transit, &[30, 22]);

    assert_eq!(
        handler(&world).execute(&service(), input("berlin")),
        Ok(DeliveryDurationEstimate {
            courier_lead: minutes(15),
            transit: minutes(20),
        })
    );
}

/// Each leg switches to its median on its own: `courier_lead` is measured at
/// Pickup, before the transit of the same Delivery ends.
#[test]
fn legs_switch_to_the_median_independently() {
    let world = World::new();
    sample(&world, &berlin(), DurationKind::CourierLead, &[11, 11, 11]);

    assert_eq!(
        handler(&world).execute(&service(), input("berlin")),
        Ok(DeliveryDurationEstimate {
            courier_lead: minutes(11),
            transit: minutes(20),
        })
    );
}

#[test]
fn empty_or_malformed_market_is_invalid() {
    let world = World::new();
    for (market, error) in [
        ("", MarketIdError::Empty),
        ("Berlin", MarketIdError::InvalidFormat),
    ] {
        assert_eq!(
            handler(&world).execute(&service(), input(market)),
            Err(DeliveryApplicationError::InvalidRequest(
                InvalidDeliveryRequest::Market(error)
            ))
        );
    }
}

#[test]
fn denied_subject_reads_nothing() {
    let world = World::with_authorization(StaticAuthorization::deny_all());

    assert_eq!(
        handler(&world).execute(&service(), input("berlin")),
        Err(DeliveryApplicationError::PermissionDenied)
    );
}

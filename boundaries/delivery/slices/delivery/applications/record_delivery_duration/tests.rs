use std::time::Duration;

use super::*;
use crate::infrastructure::repository::test_support::TestStore;
use crate::slices::delivery::applications::estimate_delivery_duration::{
    self, DeliveryDurationSamples,
};
use crate::slices::delivery::applications::test_support::{World, berlin, hamburg, service};
use crate::slices::delivery::domains::delivery::{Delivery, OrderId};

/// Creation time of the seeded deliveries, epoch seconds.
const CREATED_AT: i64 = 1_800_000_000;
const MINUTE_MS: i64 = 60_000;

fn minutes(value: u64) -> Duration {
    Duration::from_secs(value * 60)
}

/// Moment `offset` minutes after the creation of the seeded deliveries.
fn at(offset: i64) -> i64 {
    CREATED_AT * 1000 + offset * MINUTE_MS
}

fn policy() -> DeliveryEstimatePolicy {
    DeliveryEstimatePolicy::new(3, minutes(15), minutes(20)).expect("policy")
}

fn handler(world: &World) -> Handler<'_, TestStore> {
    Handler {
        unit_of_work: &world.store,
        inbox: &world.store,
        statistics: &world.store,
        deliveries: &world.store,
        policy: policy(),
    }
}

fn created(world: &World, id: &str, market_id: &MarketId) {
    DeliveryRepository::add(
        &world.store,
        &Delivery::create(
            DeliveryId::new(id),
            OrderId::new(format!("order-of-{id}")),
            market_id.clone(),
            CREATED_AT,
        ),
    )
    .expect("seed delivery");
}

fn fact(event_id: &str, delivery: &str, fact: Fact, occurred_at_ms: i64) -> Input {
    let event_type = match fact {
        Fact::PickedUp => "nomnom.delivery.v1.DeliveryPickedUp",
        Fact::Completed => "nomnom.delivery.v1.DeliveryCompleted",
        Fact::Dropped => "nomnom.delivery.v1.DeliveryCancelled",
    };
    Input {
        event_id: event_id.to_owned(),
        event_type: event_type.to_owned(),
        occurred_at_ms,
        delivery_id: delivery.to_owned(),
        fact,
    }
}

fn latest(world: &World, market_id: &MarketId, kind: DurationKind) -> Vec<Duration> {
    world
        .store
        .latest_durations(market_id, kind, 10)
        .expect("read samples")
}

/// AC-904: deliveries of the Market fill the sample through the consumer;
/// the estimate is the median of the sample.
#[test]
fn completed_deliveries_fill_the_sample_and_give_the_median() {
    let world = World::new();
    for (index, (lead, transit)) in [(8, 20), (10, 22), (12, 30)].into_iter().enumerate() {
        let delivery = format!("delivery-{index}");
        created(&world, &delivery, &berlin());
        handler(&world)
            .execute(fact(
                &format!("pickup-{index}"),
                &delivery,
                Fact::PickedUp,
                at(lead),
            ))
            .expect("pickup recorded");
        handler(&world)
            .execute(fact(
                &format!("completed-{index}"),
                &delivery,
                Fact::Completed,
                at(lead + transit),
            ))
            .expect("handover recorded");
    }

    let estimate = estimate_delivery_duration::Handler {
        authorization: &world.authorization,
        samples: &world.store,
        policy: policy(),
    }
    .execute(
        &service(),
        estimate_delivery_duration::Input {
            market_id: "berlin".to_owned(),
        },
    )
    .expect("estimate");

    assert_eq!(estimate.transit, minutes(22));
    assert_eq!(estimate.courier_lead, minutes(10));
    assert!(latest(&world, &hamburg(), DurationKind::Transit).is_empty());
}

/// AC-935
#[test]
fn delivery_cancelled_after_pickup_leaves_without_transit() {
    let world = World::new();
    created(&world, "delivery-1", &berlin());
    handler(&world)
        .execute(fact("pickup", "delivery-1", Fact::PickedUp, at(10)))
        .expect("pickup recorded");

    let output = handler(&world)
        .execute(fact("cancelled", "delivery-1", Fact::Dropped, at(25)))
        .expect("cancellation recorded");
    // A later handover finds no open transit.
    handler(&world)
        .execute(fact("completed", "delivery-1", Fact::Completed, at(40)))
        .expect("handover recorded");

    assert_eq!(output, Output::Recorded);
    assert!(latest(&world, &berlin(), DurationKind::Transit).is_empty());
    assert_eq!(
        latest(&world, &berlin(), DurationKind::CourierLead),
        vec![minutes(10)]
    );
    assert_eq!(
        world.store.close_transit(&DeliveryId::new("delivery-1")),
        Ok(None)
    );
}

/// FR-676: a repeated event changes nothing.
#[test]
fn repeated_events_do_not_change_the_statistics() {
    let world = World::new();
    created(&world, "delivery-1", &berlin());
    let pickup = fact("pickup", "delivery-1", Fact::PickedUp, at(10));
    let completed = fact("completed", "delivery-1", Fact::Completed, at(30));
    handler(&world).execute(pickup.clone()).expect("pickup");
    handler(&world)
        .execute(completed.clone())
        .expect("handover");

    assert_eq!(handler(&world).execute(pickup), Ok(Output::Duplicate));
    assert_eq!(handler(&world).execute(completed), Ok(Output::Duplicate));

    assert_eq!(
        latest(&world, &berlin(), DurationKind::CourierLead),
        vec![minutes(10)]
    );
    assert_eq!(
        latest(&world, &berlin(), DurationKind::Transit),
        vec![minutes(20)]
    );
    // The repeated Pickup did not reopen the transit.
    assert_eq!(
        world.store.close_transit(&DeliveryId::new("delivery-1")),
        Ok(None)
    );
}

#[test]
fn sample_keeps_the_latest_durations_of_the_market() {
    let world = World::new();
    for index in 0..5 {
        let delivery = format!("delivery-{index}");
        created(&world, &delivery, &berlin());
        handler(&world)
            .execute(fact(
                &format!("pickup-{index}"),
                &delivery,
                Fact::PickedUp,
                at(index + 1),
            ))
            .expect("pickup recorded");
    }

    assert_eq!(
        latest(&world, &berlin(), DurationKind::CourierLead),
        vec![minutes(5), minutes(4), minutes(3)]
    );
}

#[test]
fn transit_is_kept_in_the_market_of_the_delivery() {
    let world = World::new();
    created(&world, "delivery-1", &hamburg());
    handler(&world)
        .execute(fact("pickup", "delivery-1", Fact::PickedUp, at(10)))
        .expect("pickup recorded");
    handler(&world)
        .execute(fact("completed", "delivery-1", Fact::Completed, at(28)))
        .expect("handover recorded");

    assert_eq!(
        latest(&world, &hamburg(), DurationKind::Transit),
        vec![minutes(18)]
    );
    assert!(latest(&world, &berlin(), DurationKind::Transit).is_empty());
}

/// A Delivery stored before its creation time was recorded has no
/// `courier_lead`; its transit is still measured.
#[test]
fn delivery_without_creation_time_has_no_courier_lead() {
    let world = World::new();
    world.waiting_delivery("delivery-1");
    handler(&world)
        .execute(fact("pickup", "delivery-1", Fact::PickedUp, at(10)))
        .expect("pickup recorded");
    handler(&world)
        .execute(fact("completed", "delivery-1", Fact::Completed, at(31)))
        .expect("handover recorded");

    assert!(latest(&world, &berlin(), DurationKind::CourierLead).is_empty());
    assert_eq!(
        latest(&world, &berlin(), DurationKind::Transit),
        vec![minutes(21)]
    );
}

#[test]
fn pickup_of_an_unknown_delivery_records_nothing() {
    let world = World::new();

    assert_eq!(
        handler(&world).execute(fact("pickup", "delivery-1", Fact::PickedUp, at(10))),
        Err(DeliveryApplicationError::DeliveryNotFound(DeliveryId::new(
            "delivery-1"
        )))
    );
    // The rolled back claim lets a redelivery process the event.
    created(&world, "delivery-1", &berlin());
    assert_eq!(
        handler(&world).execute(fact("pickup", "delivery-1", Fact::PickedUp, at(10))),
        Ok(Output::Recorded)
    );
}

#[test]
fn storage_outage_is_a_dependency_failure() {
    let world = World::new();
    created(&world, "delivery-1", &berlin());
    world.store.set_unavailable(true);

    assert!(matches!(
        handler(&world).execute(fact("pickup", "delivery-1", Fact::PickedUp, at(10))),
        Err(DeliveryApplicationError::DependencyUnavailable(_))
    ));
}

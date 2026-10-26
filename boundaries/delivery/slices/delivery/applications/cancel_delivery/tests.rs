use super::*;
use crate::infrastructure::memory::{
    RecordingRelationships, RelationshipWrite, StaticAuthorization,
};
use crate::infrastructure::repository::test_support::TestStore;
use crate::slices::courier::domains::courier::CourierStatus;
use crate::slices::courier::domains::courier::MarketId;
use crate::slices::delivery::applications::error::InvalidDeliveryRequest;
use crate::slices::delivery::applications::outbox::OutboxMessage;
use crate::slices::delivery::applications::test_support::{World, new_delivery, service};
use crate::slices::delivery::domains::delivery::{
    DeliveryFailureReason, DomainEvent, FailureReporter,
};

fn handler(world: &World) -> Handler<'_, TestStore> {
    Handler {
        authorization: &world.authorization,
        unit_of_work: &world.store,
        deliveries: &world.store,
        couriers: &world.store,
        outbox: &world.store,
        relationships: &world.relationships,
    }
}

fn input() -> Input {
    Input {
        order_id: "order-of-delivery-1".to_owned(),
        reason: " order  cancelled ".to_owned(),
    }
}

/// AC-455
#[test]
fn assigned_delivery_is_cancelled_and_courier_released_repeat_succeeds() {
    let world = World::new();
    world.assigned_delivery("delivery-1", "courier-1");

    let output = handler(&world)
        .execute(&service(), input())
        .expect("cancelled");

    assert_eq!(
        output,
        Output {
            delivery_id: DeliveryId::new("delivery-1"),
            outcome: Outcome::Cancelled {
                released_courier_id: Some(CourierId::new("courier-1")),
                already_cancelled: false,
            },
        }
    );
    let delivery = world.delivery("delivery-1");
    assert_eq!(delivery.state(), State::Cancelled);
    assert_eq!(delivery.courier_id(), None);
    assert_eq!(world.courier_status("courier-1"), CourierStatus::Available);
    assert_eq!(
        world.store.outbox_messages(),
        vec![OutboxMessage::delivery(DomainEvent::DeliveryCancelled {
            delivery_id: DeliveryId::new("delivery-1"),
            order_id: OrderId::new("order-of-delivery-1"),
            released_courier_id: Some(CourierId::new("courier-1")),
            reason: DeliveryCancellationReason::new("order cancelled"),
            market_id: MarketId::new("berlin").expect("market id"),
        })]
    );

    let repeat = handler(&world)
        .execute(&service(), input())
        .expect("repeat succeeds");
    assert_eq!(
        repeat.outcome,
        Outcome::Cancelled {
            released_courier_id: None,
            already_cancelled: true,
        }
    );
    assert_eq!(world.store.outbox_messages().len(), 1);
    assert_eq!(world.courier_status("courier-1"), CourierStatus::Available);
    // The repeat changes nothing and writes nothing.
    assert_eq!(world.relationships.writes(), vec![unassigned()]);
    // FR-447: the revision of the DELETE is stored and kept by the repeat.
    assert_eq!(
        world.delivery("delivery-1").authorization_revision(),
        Some(&RecordingRelationships::revision(1))
    );
}

fn unassigned() -> RelationshipWrite {
    RelationshipWrite::UnassignCourier(DeliveryId::new("delivery-1"), CourierId::new("courier-1"))
}

/// FR-444: a failed delete rolls the cancellation back (fail closed).
#[test]
fn failed_relationship_delete_rolls_cancellation_back() {
    let world = World {
        relationships: RecordingRelationships::failing("spicedb offline"),
        ..World::new()
    };
    world.assigned_delivery("delivery-1", "courier-1");

    assert!(matches!(
        handler(&world).execute(&service(), input()),
        Err(DeliveryApplicationError::DependencyUnavailable(_))
    ));
    assert_eq!(world.delivery_state("delivery-1"), State::Assigned);
    assert_eq!(world.courier_status("courier-1"), CourierStatus::Busy);
    assert!(world.store.outbox_messages().is_empty());
}

#[test]
fn waiting_delivery_is_cancelled_without_courier() {
    let world = World::new();
    world.waiting_delivery("delivery-1");

    let output = handler(&world)
        .execute(
            &service(),
            Input {
                reason: "  ".to_owned(),
                ..input()
            },
        )
        .expect("cancelled");

    assert_eq!(
        output.outcome,
        Outcome::Cancelled {
            released_courier_id: None,
            already_cancelled: false,
        }
    );
    assert_eq!(world.delivery_state("delivery-1"), State::Cancelled);
    assert_eq!(
        world.store.outbox_messages(),
        vec![OutboxMessage::delivery(DomainEvent::DeliveryCancelled {
            delivery_id: DeliveryId::new("delivery-1"),
            order_id: OrderId::new("order-of-delivery-1"),
            released_courier_id: None,
            reason: None,
            market_id: MarketId::new("berlin").expect("market id"),
        })]
    );
    assert!(world.relationships.writes().is_empty());
}

/// AC-456
#[test]
fn picked_up_delivery_is_rejected_as_already_picked_up() {
    let world = World::new();
    world.picked_up_delivery("delivery-1", "courier-1");

    let output = handler(&world)
        .execute(&service(), input())
        .expect("business rejection is not an error");

    assert_eq!(
        output.outcome,
        Outcome::Rejected(CancellationRejection::AlreadyPickedUp)
    );
    assert_eq!(world.delivery_state("delivery-1"), State::PickedUp);
    assert_eq!(world.courier_status("courier-1"), CourierStatus::Busy);
    assert!(world.store.outbox_messages().is_empty());
    assert!(world.relationships.writes().is_empty());
}

#[test]
fn closed_delivery_is_rejected_as_already_closed() {
    let world = World::new();
    let courier = CourierId::new("courier-1");
    world.courier("courier-1", CourierStatus::Available);

    let mut delivered = new_delivery("delivery-1");
    delivered.assign(courier.clone(), None).expect("assign");
    delivered.confirm_pickup(&courier, None).expect("pickup");
    delivered.complete(&courier).expect("complete");
    DeliveryRepository::add(&world.store, &delivered).expect("seed delivered");

    let mut failed = new_delivery("delivery-2");
    failed.assign(courier, None).expect("assign");
    failed
        .fail(
            DeliveryFailureReason::new("no_access", Some("door closed")).expect("reason"),
            &FailureReporter::Dispatcher,
        )
        .expect("fail");
    DeliveryRepository::add(&world.store, &failed).expect("seed failed");

    for (order, state) in [
        ("order-of-delivery-1", State::Delivered),
        ("order-of-delivery-2", State::Failed),
    ] {
        let output = handler(&world)
            .execute(
                &service(),
                Input {
                    order_id: order.to_owned(),
                    reason: String::new(),
                },
            )
            .expect("business rejection");
        assert_eq!(
            output.outcome,
            Outcome::Rejected(CancellationRejection::AlreadyClosed)
        );
        assert_eq!(
            DeliveryRepository::find_by_order(&world.store, &OrderId::new(order))
                .expect("read")
                .expect("exists")
                .state(),
            state
        );
    }
    assert!(world.store.outbox_messages().is_empty());
}

#[test]
fn unknown_order_is_not_found() {
    let world = World::new();
    assert_eq!(
        handler(&world).execute(&service(), input()),
        Err(DeliveryApplicationError::DeliveryForOrderNotFound(
            OrderId::new("order-of-delivery-1")
        ))
    );
}

#[test]
fn empty_order_id_is_invalid() {
    let world = World::new();
    assert_eq!(
        handler(&world).execute(
            &service(),
            Input {
                order_id: " ".to_owned(),
                reason: String::new(),
            }
        ),
        Err(DeliveryApplicationError::InvalidRequest(
            InvalidDeliveryRequest::EmptyIdentifier("order_id")
        ))
    );
}

#[test]
fn denied_subject_cancels_nothing() {
    let world = World::with_authorization(StaticAuthorization::deny_all());
    world.assigned_delivery("delivery-1", "courier-1");

    assert_eq!(
        handler(&world).execute(&service(), input()),
        Err(DeliveryApplicationError::PermissionDenied)
    );
    assert_eq!(
        world.authorization.delivery_checks(),
        vec![(
            service(),
            DeliveryPermission::CancelDelivery,
            DeliveryResource::Order(OrderId::new("order-of-delivery-1")),
        )]
    );
    assert_eq!(world.delivery_state("delivery-1"), State::Assigned);
    assert_eq!(world.courier_status("courier-1"), CourierStatus::Busy);
}

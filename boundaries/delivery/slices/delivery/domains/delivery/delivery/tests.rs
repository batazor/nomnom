use super::*;

fn delivery() -> Delivery {
    Delivery::new(
        DeliveryId::new("delivery-1"),
        OrderId::new("order-1"),
        Address::new(" Restaurant  street 1 ").expect("pickup address"),
        Address::new("Customer street 2").expect("dropoff address"),
    )
}

fn assigned(courier: &str) -> Delivery {
    let mut delivery = delivery();
    delivery.assign(CourierId::new(courier)).expect("assign");
    delivery.pull_events();
    delivery
}

fn reason() -> DeliveryFailureReason {
    DeliveryFailureReason::new("customer unreachable").expect("reason")
}

#[test]
fn delivery_lifecycle_is_strict_and_records_facts() {
    let mut delivery = delivery();
    let courier_id = CourierId::new("courier-1");

    assert_eq!(
        delivery.assign(courier_id.clone()).expect("assign").to,
        State::Assigned
    );
    assert_eq!(
        delivery
            .assign(courier_id.clone())
            .expect_err("duplicate assignment"),
        TransitionError::AlreadyInState {
            state: State::Assigned
        }
    );
    assert_eq!(
        delivery.confirm_pickup(&courier_id).expect("pickup").to,
        State::PickedUp
    );
    assert_eq!(
        delivery.complete(&courier_id).expect("completion").to,
        State::Delivered
    );
    let order_id = OrderId::new("order-1");
    let delivery_id = DeliveryId::new("delivery-1");
    assert_eq!(
        delivery.pull_events(),
        vec![
            DomainEvent::CourierAssignedToDelivery {
                delivery_id: delivery_id.clone(),
                order_id: order_id.clone(),
                courier_id: courier_id.clone(),
            },
            DomainEvent::DeliveryPickedUp {
                delivery_id: delivery_id.clone(),
                order_id: order_id.clone(),
                courier_id: courier_id.clone(),
            },
            DomainEvent::DeliveryCompleted {
                delivery_id,
                order_id,
                courier_id,
            },
        ]
    );
    assert!(delivery.pull_events().is_empty());
}

#[test]
fn invalid_transition_does_not_change_state() {
    let mut delivery = delivery();
    assert_eq!(
        delivery
            .confirm_pickup(&CourierId::new("courier-1"))
            .expect_err("pickup before assignment"),
        DeliveryError::Transition(TransitionError::InvalidTransition {
            from: State::Waiting,
            trigger: Trigger::Pickup
        })
    );
    assert_eq!(delivery.state(), State::Waiting);
    assert!(delivery.pull_events().is_empty());
}

#[test]
fn only_assigned_courier_confirms_pickup_and_completion() {
    let mut delivery = assigned("courier-1");
    let other = CourierId::new("courier-2");

    assert_eq!(
        delivery.confirm_pickup(&other),
        Err(DeliveryError::CourierNotAssigned)
    );
    assert_eq!(delivery.state(), State::Assigned);

    delivery
        .confirm_pickup(&CourierId::new("courier-1"))
        .expect("pickup");
    assert_eq!(
        delivery.complete(&other),
        Err(DeliveryError::CourierNotAssigned)
    );
    assert_eq!(delivery.state(), State::PickedUp);
}

#[test]
fn unassign_returns_delivery_to_waiting() {
    let mut delivery = assigned("courier-1");

    assert_eq!(delivery.unassign().expect("unassign").to, State::Waiting);
    assert_eq!(delivery.courier_id(), None);
    assert_eq!(
        delivery.pull_events(),
        vec![DomainEvent::CourierUnassignedFromDelivery {
            delivery_id: DeliveryId::new("delivery-1"),
            order_id: OrderId::new("order-1"),
            courier_id: CourierId::new("courier-1"),
        }]
    );
    assert_eq!(
        delivery.unassign(),
        Err(TransitionError::InvalidTransition {
            from: State::Waiting,
            trigger: Trigger::Unassign
        })
    );
}

#[test]
fn failure_is_recorded_with_reason_by_courier_or_dispatcher() {
    let mut by_courier = assigned("courier-1");
    by_courier
        .fail(
            reason(),
            &FailureReporter::AssignedCourier(CourierId::new("courier-1")),
        )
        .expect("courier reports failure");
    assert_eq!(by_courier.state(), State::Failed);
    assert_eq!(by_courier.failure_reason(), Some(&reason()));
    assert_eq!(
        by_courier.pull_events(),
        vec![DomainEvent::DeliveryFailed {
            delivery_id: DeliveryId::new("delivery-1"),
            order_id: OrderId::new("order-1"),
            courier_id: CourierId::new("courier-1"),
            reason: reason(),
        }]
    );

    let mut by_dispatcher = assigned("courier-1");
    by_dispatcher
        .fail(reason(), &FailureReporter::Dispatcher)
        .expect("dispatcher reports failure");
    assert_eq!(by_dispatcher.state(), State::Failed);

    let mut by_other = assigned("courier-1");
    assert_eq!(
        by_other.fail(
            reason(),
            &FailureReporter::AssignedCourier(CourierId::new("courier-2"))
        ),
        Err(DeliveryError::CourierNotAssigned)
    );
    assert_eq!(by_other.state(), State::Assigned);
}

#[test]
fn waiting_delivery_cannot_fail() {
    let mut delivery = delivery();
    assert_eq!(
        delivery.fail(reason(), &FailureReporter::Dispatcher),
        Err(DeliveryError::Transition(
            TransitionError::InvalidTransition {
                from: State::Waiting,
                trigger: Trigger::Fail
            }
        ))
    );
}

#[test]
fn cancellation_before_pickup_releases_the_courier() {
    let mut cancelled = assigned("courier-1");
    let reason = DeliveryCancellationReason::new("order cancelled");

    assert_eq!(
        cancelled.cancel(reason.clone()),
        Ok(Some(CourierId::new("courier-1")))
    );
    assert_eq!(cancelled.state(), State::Cancelled);
    assert_eq!(cancelled.courier_id(), None);
    assert_eq!(
        cancelled.pull_events(),
        vec![DomainEvent::DeliveryCancelled {
            delivery_id: DeliveryId::new("delivery-1"),
            order_id: OrderId::new("order-1"),
            released_courier_id: Some(CourierId::new("courier-1")),
            reason,
        }]
    );
    assert_eq!(
        cancelled.cancel(None),
        Err(TransitionError::AlreadyInState {
            state: State::Cancelled
        })
    );
    assert!(cancelled.pull_events().is_empty());

    let mut waiting = delivery();
    assert_eq!(waiting.cancel(None), Ok(None));
    assert_eq!(waiting.state(), State::Cancelled);
}

#[test]
fn picked_up_delivery_cannot_be_cancelled() {
    let mut delivery = assigned("courier-1");
    delivery
        .confirm_pickup(&CourierId::new("courier-1"))
        .expect("pickup");
    delivery.pull_events();

    assert_eq!(
        delivery.cancel(None),
        Err(TransitionError::InvalidTransition {
            from: State::PickedUp,
            trigger: Trigger::Cancel
        })
    );
    assert_eq!(delivery.state(), State::PickedUp);
    assert_eq!(delivery.courier_id(), Some(&CourierId::new("courier-1")));
    assert!(delivery.pull_events().is_empty());
}

#[test]
fn ensure_assignable_does_not_change_state() {
    let delivery = delivery();
    assert_eq!(delivery.ensure_assignable(), Ok(()));
    assert_eq!(delivery.state(), State::Waiting);
    assert_eq!(
        assigned("courier-2").ensure_assignable(),
        Err(TransitionError::AlreadyInState {
            state: State::Assigned
        })
    );
}

#[test]
fn restore_validates_courier_and_failure_reason() {
    let restore = |courier: Option<&str>, state, reason| {
        Delivery::restore(
            DeliveryId::new("delivery-1"),
            OrderId::new("order-1"),
            courier.map(CourierId::new),
            Address::new("a").expect("address"),
            Address::new("b").expect("address"),
            state,
            reason,
        )
    };
    assert_eq!(
        restore(Some("c"), State::Waiting, None).expect_err("waiting with courier"),
        DeliveryValidationError::WaitingStateMustNotHaveCourier
    );
    assert_eq!(
        restore(None, State::Assigned, None).expect_err("assigned without courier"),
        DeliveryValidationError::AssignedStateRequiresCourier
    );
    assert_eq!(
        restore(Some("c"), State::Failed, None).expect_err("failed without reason"),
        DeliveryValidationError::FailedStateRequiresReason
    );
    assert_eq!(
        restore(Some("c"), State::Delivered, Some(reason())).expect_err("reason not failed"),
        DeliveryValidationError::FailureReasonRequiresFailedState
    );
    assert_eq!(
        restore(Some("c"), State::Cancelled, None).expect_err("cancelled with courier"),
        DeliveryValidationError::CancelledStateMustNotHaveCourier
    );
    assert!(restore(None, State::Cancelled, None).is_ok());
    assert!(restore(Some("c"), State::Failed, Some(reason())).is_ok());
}

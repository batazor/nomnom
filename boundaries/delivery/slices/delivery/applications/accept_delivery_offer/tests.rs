use std::cell::RefCell;

use super::*;
use crate::infrastructure::memory::StaticAuthorization;
use crate::infrastructure::memory::{RecordingRelationships, RelationshipWrite};
use crate::infrastructure::repository::test_support::TestStore;
use crate::pkg::application::UnitOfWorkError;
use crate::slices::courier::applications::assignment::CourierAssignmentError;
use crate::slices::courier::domains::courier::CourierStatus;
use crate::slices::delivery::applications::authorization::DeliveryResource;
use crate::slices::delivery::applications::error::InvalidDeliveryRequest;
use crate::slices::delivery::applications::outbox::OutboxMessage;
use crate::slices::delivery::applications::test_support::{
    NOW, OFFER_TTL, ObservingRelationships, World, candidate, subject,
};
use crate::slices::delivery::domains::delivery::{
    DeliveryError, DeliveryOfferError, DeliveryOfferReason, DeliveryValidationError, DomainEvent,
    ExpectedTime, OfferTrigger, OrderId, State, TransitionError,
};

fn handler(world: &World) -> Handler<'_, TestStore> {
    Handler {
        authorization: &world.authorization,
        unit_of_work: &world.store,
        offers: &world.store,
        offers_of: &world.store,
        deliveries: &world.store,
        couriers: &world.store,
        outbox: &world.store,
        locator: &world.locator,
        clock: &world.clock,
        policy: world.policy,
        relationships: &world.relationships,
    }
}

fn input(offer: &str, courier: &str) -> Input {
    Input {
        offer_id: offer.to_owned(),
        courier_id: courier.to_owned(),
        expected_pickup_at_epoch_seconds: None,
    }
}

/// Waiting delivery-1 with pending offers to two available couriers.
fn world_with_two_offers() -> World {
    let world = World::new();
    world.waiting_delivery("delivery-1");
    world.courier("courier-1", CourierStatus::Available);
    world.courier("courier-2", CourierStatus::Available);
    world.offer("offer-1", "delivery-1", "courier-1");
    world.offer("offer-2", "delivery-1", "courier-2");
    world.locator.set_candidates(vec![
        candidate("courier-1", 1_000),
        candidate("courier-2", 2_000),
    ]);
    world
}

/// Facts of one acceptance of `offer-1`: the accepted offer, then the
/// Courier Assignment.
fn assignment_fact() -> Vec<OutboxMessage> {
    vec![
        OutboxMessage::delivery(DomainEvent::DeliveryOfferAccepted {
            delivery_id: DeliveryId::new("delivery-1"),
            offer_id: DeliveryOfferId::new("offer-1"),
            courier_id: CourierId::new("courier-1"),
        }),
        OutboxMessage::delivery(DomainEvent::CourierAssignedToDelivery {
            delivery_id: DeliveryId::new("delivery-1"),
            order_id: OrderId::new("order-of-delivery-1"),
            courier_id: CourierId::new("courier-1"),
            created_at_epoch_seconds: None,
            expected_pickup_at_epoch_seconds: None,
        }),
    ]
}

/// AC-323
#[test]
fn accepting_current_offer_creates_assignment_atomically() {
    let world = world_with_two_offers();

    let output = handler(&world)
        .execute(&subject("courier-1"), input("offer-1", "courier-1"))
        .expect("offer accepted");

    assert_eq!(
        output,
        Output {
            offer_id: DeliveryOfferId::new("offer-1"),
            delivery_id: DeliveryId::new("delivery-1"),
            courier_id: CourierId::new("courier-1"),
            already_accepted: false,
        }
    );
    let delivery = world.delivery("delivery-1");
    assert_eq!(delivery.state(), State::Assigned);
    assert_eq!(delivery.courier_id(), Some(&CourierId::new("courier-1")));
    assert_eq!(world.courier_status("courier-1"), CourierStatus::Busy);
    assert_eq!(world.offer_state("offer-1"), OfferState::Accepted);
    assert_eq!(world.offer_state("offer-2"), OfferState::Withdrawn);
    assert_eq!(world.store.outbox_messages(), assignment_fact());
    assert_eq!(world.store.commits(), 1);
    assert_eq!(world.relationships.writes(), vec![assigned("courier-1")]);
    // FR-447: the revision of the TOUCH is stored with the delivery.
    let revision = RecordingRelationships::revision(1);
    assert_eq!(delivery.authorization_revision(), Some(&revision));

    // A repeat writes nothing and keeps the stored revision.
    handler(&world)
        .execute(&subject("courier-1"), input("offer-1", "courier-1"))
        .expect("repeat succeeds");
    assert_eq!(world.relationships.writes().len(), 1);
    assert_eq!(
        world.delivery("delivery-1").authorization_revision(),
        Some(&revision)
    );
}

/// FR-610: the Pickup time named on acceptance is stored and reaches
/// `CourierAssignedToDelivery`.
#[test]
fn accepting_with_expected_pickup_time_records_it() {
    let world = world_with_two_offers();

    handler(&world)
        .execute(
            &subject("courier-1"),
            Input {
                expected_pickup_at_epoch_seconds: Some(NOW + 300),
                ..input("offer-1", "courier-1")
            },
        )
        .expect("offer accepted");

    assert_eq!(
        world.delivery("delivery-1").expected_pickup_at(),
        Some(ExpectedTime::restore(NOW + 300))
    );
    let mut expected = assignment_fact();
    expected[1] = OutboxMessage::delivery(DomainEvent::CourierAssignedToDelivery {
        delivery_id: DeliveryId::new("delivery-1"),
        order_id: OrderId::new("order-of-delivery-1"),
        courier_id: CourierId::new("courier-1"),
        created_at_epoch_seconds: None,
        expected_pickup_at_epoch_seconds: Some(NOW + 300),
    });
    assert_eq!(world.store.outbox_messages(), expected);
}

/// FR-610: a named Pickup time not in the future is an invalid request and
/// changes nothing.
#[test]
fn pickup_time_not_in_the_future_is_rejected() {
    let world = world_with_two_offers();

    assert_eq!(
        handler(&world).execute(
            &subject("courier-1"),
            Input {
                expected_pickup_at_epoch_seconds: Some(NOW),
                ..input("offer-1", "courier-1")
            },
        ),
        Err(DeliveryApplicationError::InvalidRequest(
            InvalidDeliveryRequest::Validation(DeliveryValidationError::ExpectedTimeNotInFuture {
                field: "expected_pickup_at"
            })
        ))
    );
    assert_eq!(world.delivery_state("delivery-1"), State::Waiting);
    assert_eq!(world.offer_state("offer-1"), OfferState::Pending);
    assert!(world.store.outbox_messages().is_empty());
}

fn assigned(courier: &str) -> RelationshipWrite {
    RelationshipWrite::AssignCourier(DeliveryId::new("delivery-1"), CourierId::new(courier))
}

/// FR-444: `delivery#courier` is written inside the Unit of Work before the
/// delivery is saved.
#[test]
fn assignment_relationship_is_written_before_delivery_is_saved() {
    let world = world_with_two_offers();
    let writer = ObservingRelationships::new(&world.store);
    let handler = Handler {
        relationships: &writer,
        ..handler(&world)
    };

    handler
        .execute(&subject("courier-1"), input("offer-1", "courier-1"))
        .expect("offer accepted");

    assert_eq!(
        *writer.observed.borrow(),
        vec![(assigned("courier-1"), None)]
    );
    assert_eq!(
        world.delivery("delivery-1").courier_id(),
        Some(&CourierId::new("courier-1"))
    );
}

/// FR-444: a failed write rolls the acceptance back (fail closed).
#[test]
fn failed_relationship_write_rolls_acceptance_back() {
    let world = World {
        relationships: RecordingRelationships::failing("spicedb offline"),
        ..world_with_two_offers()
    };

    assert!(matches!(
        handler(&world).execute(&subject("courier-1"), input("offer-1", "courier-1")),
        Err(DeliveryApplicationError::DependencyUnavailable(reason)) if reason.contains("spicedb offline")
    ));
    assert_eq!(world.delivery_state("delivery-1"), State::Waiting);
    assert_eq!(world.delivery("delivery-1").courier_id(), None);
    assert_eq!(world.offer_state("offer-1"), OfferState::Pending);
    assert_eq!(world.offer_state("offer-2"), OfferState::Pending);
    assert_eq!(world.courier_status("courier-1"), CourierStatus::Available);
    assert!(world.store.outbox_messages().is_empty());
    assert_eq!(world.store.rollbacks(), 1);
}

/// AC-324
#[test]
fn second_courier_cannot_accept_already_assigned_delivery() {
    let world = world_with_two_offers();
    handler(&world)
        .execute(&subject("courier-1"), input("offer-1", "courier-1"))
        .expect("first acceptance");

    assert_eq!(
        handler(&world).execute(&subject("courier-2"), input("offer-2", "courier-2")),
        Err(DeliveryApplicationError::OfferPrecondition(
            DeliveryOfferError::InvalidTransition {
                from: OfferState::Withdrawn,
                trigger: OfferTrigger::Accept,
            }
        ))
    );
    assert_eq!(
        world.delivery("delivery-1").courier_id(),
        Some(&CourierId::new("courier-1"))
    );
    assert_eq!(world.courier_status("courier-2"), CourierStatus::Available);
    assert_eq!(world.store.outbox_messages(), assignment_fact());
    assert_eq!(world.relationships.writes(), vec![assigned("courier-1")]);
}

#[test]
fn stale_pending_offer_of_assigned_delivery_is_rejected_by_delivery() {
    let world = World::new();
    world.assigned_delivery("delivery-1", "courier-1");
    world.courier("courier-2", CourierStatus::Available);
    world.offer("offer-2", "delivery-1", "courier-2");
    world
        .locator
        .set_candidates(vec![candidate("courier-2", 1_000)]);

    assert_eq!(
        handler(&world).execute(&subject("courier-2"), input("offer-2", "courier-2")),
        Err(DeliveryApplicationError::DeliveryPrecondition(
            DeliveryError::Transition(TransitionError::AlreadyInState {
                state: State::Assigned
            })
        ))
    );
    assert_eq!(world.offer_state("offer-2"), OfferState::Pending);
    assert_eq!(world.courier_status("courier-2"), CourierStatus::Available);
    assert!(world.store.outbox_messages().is_empty());
    assert_eq!(world.store.rollbacks(), 1);
    assert!(world.relationships.writes().is_empty());
}

/// AC-325
#[test]
fn expired_offer_is_rejected_with_reason() {
    let world = world_with_two_offers();
    world.clock.set(NOW + OFFER_TTL + 1);

    assert_eq!(
        handler(&world).execute(&subject("courier-1"), input("offer-1", "courier-1")),
        Err(DeliveryApplicationError::OfferPrecondition(
            DeliveryOfferError::Unacceptable {
                reasons: vec![DeliveryOfferReason::Expired]
            }
        ))
    );
    assert_eq!(world.delivery_state("delivery-1"), State::Waiting);
    assert_eq!(world.offer_state("offer-1"), OfferState::Pending);
}

/// AC-326
#[test]
fn offer_of_another_courier_is_rejected() {
    let world = world_with_two_offers();

    assert_eq!(
        handler(&world).execute(&subject("courier-2"), input("offer-1", "courier-2")),
        Err(DeliveryApplicationError::OfferPrecondition(
            DeliveryOfferError::NotAddressedToCourier
        ))
    );
    assert_eq!(world.offer_state("offer-1"), OfferState::Pending);
    assert_eq!(world.delivery_state("delivery-1"), State::Waiting);
}

/// AC-327
#[test]
fn repeated_acceptance_is_idempotent_without_second_fact() {
    let world = world_with_two_offers();
    handler(&world)
        .execute(&subject("courier-1"), input("offer-1", "courier-1"))
        .expect("first acceptance");

    let repeat = handler(&world)
        .execute(&subject("courier-1"), input("offer-1", "courier-1"))
        .expect("repeat");

    assert!(repeat.already_accepted);
    assert_eq!(world.store.outbox_messages(), assignment_fact());
    assert_eq!(world.store.commits(), 1);
    assert_eq!(world.relationships.writes(), vec![assigned("courier-1")]);
}

/// AC-328
#[test]
fn failed_commit_persists_nothing() {
    let world = world_with_two_offers();
    world
        .store
        .fail_next_commit(UnitOfWorkError::CommitFailed("connection lost".to_owned()));

    assert_eq!(
        handler(&world).execute(&subject("courier-1"), input("offer-1", "courier-1")),
        Err(DeliveryApplicationError::Transaction(
            UnitOfWorkError::CommitFailed("connection lost".to_owned())
        ))
    );
    assert_eq!(world.offer_state("offer-1"), OfferState::Pending);
    assert_eq!(world.offer_state("offer-2"), OfferState::Pending);
    assert_eq!(world.delivery_state("delivery-1"), State::Waiting);
    assert_eq!(world.courier_status("courier-1"), CourierStatus::Available);
    assert!(world.store.outbox_messages().is_empty());
}

/// Courier busyness port that records every call and delegates to the
/// store, proving the use case changes the courier only through the port.
struct RecordingAssignment<'a> {
    store: &'a TestStore,
    calls: RefCell<Vec<(&'static str, CourierId)>>,
}

impl<'a> RecordingAssignment<'a> {
    fn new(store: &'a TestStore) -> Self {
        Self {
            store,
            calls: RefCell::new(Vec::new()),
        }
    }

    fn record(&self, call: &'static str, courier_id: &CourierId) {
        self.calls.borrow_mut().push((call, courier_id.clone()));
    }
}

impl CourierAssignment for RecordingAssignment<'_> {
    fn status(&self, courier_id: &CourierId) -> Result<CourierStatus, CourierAssignmentError> {
        self.record("status", courier_id);
        self.store.status(courier_id)
    }

    fn ensure_available(&self, courier_id: &CourierId) -> Result<(), CourierAssignmentError> {
        self.record("ensure_available", courier_id);
        self.store.ensure_available(courier_id)
    }

    fn occupy(&self, courier_id: &CourierId) -> Result<(), CourierAssignmentError> {
        self.record("occupy", courier_id);
        self.store.occupy(courier_id)
    }

    fn release(&self, courier_id: &CourierId) -> Result<(), CourierAssignmentError> {
        self.record("release", courier_id);
        self.store.release(courier_id)
    }
}

/// AC-549
#[test]
fn acceptance_occupies_courier_through_port_in_one_transaction() {
    let world = world_with_two_offers();
    let couriers = RecordingAssignment::new(&world.store);
    let handler = Handler {
        couriers: &couriers,
        ..handler(&world)
    };

    handler
        .execute(&subject("courier-1"), input("offer-1", "courier-1"))
        .expect("offer accepted");

    assert_eq!(
        *couriers.calls.borrow(),
        vec![("occupy", CourierId::new("courier-1"))]
    );
    assert_eq!(world.courier_status("courier-1"), CourierStatus::Busy);
    assert_eq!(world.delivery_state("delivery-1"), State::Assigned);
    assert_eq!(world.offer_state("offer-1"), OfferState::Accepted);
    assert_eq!(world.store.outbox_messages(), assignment_fact());
    assert_eq!(world.store.commits(), 1);
}

/// AC-550
#[test]
fn failed_commit_leaves_courier_occupied_through_port_available() {
    let world = world_with_two_offers();
    let couriers = RecordingAssignment::new(&world.store);
    let handler = Handler {
        couriers: &couriers,
        ..handler(&world)
    };
    world
        .store
        .fail_next_commit(UnitOfWorkError::CommitFailed("connection lost".to_owned()));

    assert_eq!(
        handler.execute(&subject("courier-1"), input("offer-1", "courier-1")),
        Err(DeliveryApplicationError::Transaction(
            UnitOfWorkError::CommitFailed("connection lost".to_owned())
        ))
    );
    assert_eq!(
        *couriers.calls.borrow(),
        vec![("occupy", CourierId::new("courier-1"))]
    );
    assert_eq!(world.courier_status("courier-1"), CourierStatus::Available);
    assert_eq!(world.offer_state("offer-1"), OfferState::Pending);
    assert_eq!(world.delivery_state("delivery-1"), State::Waiting);
    assert!(world.store.outbox_messages().is_empty());
}

/// AC-312
#[test]
fn subject_not_acting_for_courier_is_denied_without_changes() {
    let authorization = StaticAuthorization::grants_only();
    authorization.grant_delivery(
        subject("courier-1"),
        DeliveryPermission::AcceptDeliveryOffer,
        DeliveryResource::Courier(CourierId::new("courier-1")),
    );
    let world = World::with_authorization(authorization);
    world.waiting_delivery("delivery-1");
    world.courier("courier-1", CourierStatus::Available);
    world.offer("offer-1", "delivery-1", "courier-1");
    world
        .locator
        .set_candidates(vec![candidate("courier-1", 1_000)]);

    assert_eq!(
        handler(&world).execute(&subject("intruder"), input("offer-1", "courier-1")),
        Err(DeliveryApplicationError::PermissionDenied)
    );
    assert_eq!(world.offer_state("offer-1"), OfferState::Pending);
    assert_eq!(world.delivery_state("delivery-1"), State::Waiting);

    handler(&world)
        .execute(&subject("courier-1"), input("offer-1", "courier-1"))
        .expect("granted courier accepts");
}

#[test]
fn unknown_courier_position_blocks_acceptance() {
    let world = world_with_two_offers();
    world.locator.set_candidates(Vec::new());

    assert_eq!(
        handler(&world).execute(&subject("courier-1"), input("offer-1", "courier-1")),
        Err(DeliveryApplicationError::CourierPositionUnknown(
            CourierId::new("courier-1")
        ))
    );
    assert_eq!(world.offer_state("offer-1"), OfferState::Pending);
}

#[test]
fn unknown_offer_is_not_found() {
    let world = World::new();
    assert_eq!(
        handler(&world).execute(&subject("courier-1"), input("offer-9", "courier-1")),
        Err(DeliveryApplicationError::DeliveryOfferNotFound(
            DeliveryOfferId::new("offer-9")
        ))
    );
}

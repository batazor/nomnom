//! Offer Delivery: the assignment process offers a waiting Delivery to an
//! available courier for a limited time. No Courier Assignment is created.
//! The offer and its `DeliveryOffered` fact are written in one Unit of Work.
//!
//! [`make_offer`] is the application service without authorization; Replan
//! Delivery and Dispatch Deliveries reuse it under their own Unit of Work.

use crate::pkg::application::{Clock, IdGenerator, Subject, UnitOfWork};
use crate::slices::courier::applications::assignment::CourierAssignment;
use crate::slices::courier::domains::courier::CourierId;
use crate::slices::delivery::applications::offers_of_delivery::{OffersOfDelivery, load_offers_of};
use crate::slices::delivery::domains::delivery::{
    Delivery, DeliveryId, DeliveryOffer, DeliveryOfferId, DeliveryOfferRepository,
    DeliveryRepository,
};

use super::authorization::{DeliveryAuthorization, DeliveryPermission, DeliveryResource};
use super::error::DeliveryApplicationError;
use super::outbox::{DeliveryOutbox, publish};
use super::policy::AssignmentPolicy;
use super::{courier_id, delivery_id, load_delivery};

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct Input {
    pub delivery_id: String,
    pub courier_id: String,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct Output {
    pub offer_id: DeliveryOfferId,
    pub delivery_id: DeliveryId,
    pub courier_id: CourierId,
    pub expires_at_epoch_seconds: i64,
    /// `false` when an open offer for the same pair already existed.
    pub created: bool,
}

pub struct Handler<'a, U: UnitOfWork> {
    pub authorization: &'a dyn DeliveryAuthorization,
    pub unit_of_work: &'a U,
    pub deliveries: &'a dyn DeliveryRepository,
    pub couriers: &'a dyn CourierAssignment,
    pub offers: &'a dyn DeliveryOfferRepository,
    pub offers_of: &'a dyn OffersOfDelivery,
    pub outbox: &'a dyn DeliveryOutbox,
    pub ids: &'a dyn IdGenerator,
    pub clock: &'a dyn Clock,
    pub policy: AssignmentPolicy,
}

impl<U: UnitOfWork> Handler<'_, U> {
    pub fn execute(
        &self,
        subject: &Subject,
        input: Input,
    ) -> Result<Output, DeliveryApplicationError> {
        let delivery_id = delivery_id(&input.delivery_id)?;
        let courier_id = courier_id(&input.courier_id)?;
        self.authorization.check(
            subject,
            DeliveryPermission::OfferDelivery,
            &DeliveryResource::Delivery(delivery_id.clone()),
        )?;

        self.unit_of_work.run(|| {
            let delivery = load_delivery(self.deliveries, &delivery_id)?;
            make_offer(
                &delivery,
                courier_id,
                &OfferPorts {
                    couriers: self.couriers,
                    offers: self.offers,
                    offers_of: self.offers_of,
                    outbox: self.outbox,
                    ids: self.ids,
                    clock: self.clock,
                    policy: self.policy,
                },
            )
        })
    }
}

/// Ports and policy of [`make_offer`]; its writes join the Unit of Work of
/// the caller.
pub struct OfferPorts<'a> {
    pub couriers: &'a dyn CourierAssignment,
    pub offers: &'a dyn DeliveryOfferRepository,
    pub offers_of: &'a dyn OffersOfDelivery,
    pub outbox: &'a dyn DeliveryOutbox,
    pub ids: &'a dyn IdGenerator,
    pub clock: &'a dyn Clock,
    pub policy: AssignmentPolicy,
}

/// Offers an assignable delivery to an available courier and writes the
/// `DeliveryOffered` fact; an open offer for the same pair is returned
/// instead of a new one.
pub fn make_offer(
    delivery: &Delivery,
    courier_id: CourierId,
    ports: &OfferPorts<'_>,
) -> Result<Output, DeliveryApplicationError> {
    delivery.ensure_assignable()?;
    ports.couriers.ensure_available(&courier_id)?;

    let now = ports.clock.now_epoch_seconds();
    let existing = load_offers_of(ports.offers_of, ports.offers, delivery.id())?;
    if let Some(open) = existing
        .iter()
        .find(|offer| offer.courier_id() == &courier_id && offer.is_open_at(now))
    {
        return Ok(output(open, false));
    }

    let mut offer = DeliveryOffer::new(
        DeliveryOfferId::new(ports.ids.next_id()),
        delivery.id().clone(),
        courier_id,
        now,
        now.saturating_add(ports.policy.offer_ttl_seconds),
    )?;
    ports.offers.add(&offer)?;
    publish(ports.outbox, offer.pull_events())?;
    Ok(output(&offer, true))
}

fn output(offer: &DeliveryOffer, created: bool) -> Output {
    Output {
        offer_id: offer.id().clone(),
        delivery_id: offer.delivery_id().clone(),
        courier_id: offer.courier_id().clone(),
        expires_at_epoch_seconds: offer.expires_at_epoch_seconds(),
        created,
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::infrastructure::memory::StaticAuthorization;
    use crate::infrastructure::repository::test_support::TestStore;
    use crate::slices::courier::domains::courier::{CourierError, CourierStatus};
    use crate::slices::delivery::applications::outbox::OutboxMessage;
    use crate::slices::delivery::applications::test_support::{NOW, OFFER_TTL, World, service};
    use crate::slices::delivery::domains::delivery::{
        DeliveryError, DomainEvent, OfferState, State, TransitionError,
    };

    fn handler(world: &World) -> Handler<'_, TestStore> {
        Handler {
            authorization: &world.authorization,
            unit_of_work: &world.store,
            deliveries: &world.store,
            couriers: &world.store,
            offers: &world.store,
            offers_of: &world.store,
            outbox: &world.store,
            ids: &world.ids,
            clock: &world.clock,
            policy: world.policy,
        }
    }

    fn input(courier: &str) -> Input {
        Input {
            delivery_id: "delivery-1".to_owned(),
            courier_id: courier.to_owned(),
        }
    }

    /// AC-320
    #[test]
    fn creates_pending_time_limited_offer_without_assignment() {
        let world = World::new();
        world.waiting_delivery("delivery-1");
        world.courier("courier-1", CourierStatus::Available);

        let output = handler(&world)
            .execute(&service(), input("courier-1"))
            .expect("offer created");

        assert_eq!(
            output,
            Output {
                offer_id: DeliveryOfferId::new("generated-1"),
                delivery_id: DeliveryId::new("delivery-1"),
                courier_id: CourierId::new("courier-1"),
                expires_at_epoch_seconds: NOW + OFFER_TTL,
                created: true,
            }
        );
        assert_eq!(world.offer_state("generated-1"), OfferState::Pending);
        assert_eq!(world.delivery_state("delivery-1"), State::Waiting);
        assert_eq!(world.courier_status("courier-1"), CourierStatus::Available);
        assert_eq!(
            world.store.outbox_messages(),
            vec![OutboxMessage::delivery(DomainEvent::DeliveryOffered {
                delivery_id: DeliveryId::new("delivery-1"),
                offer_id: DeliveryOfferId::new("generated-1"),
                courier_id: CourierId::new("courier-1"),
                expires_at_epoch_seconds: NOW + OFFER_TTL,
            })]
        );
        assert_eq!(world.store.commits(), 1);
    }

    /// AC-321
    #[test]
    fn repeated_offer_to_same_courier_returns_open_offer() {
        let world = World::new();
        world.waiting_delivery("delivery-1");
        world.courier("courier-1", CourierStatus::Available);
        handler(&world)
            .execute(&service(), input("courier-1"))
            .expect("first");
        world.clock.advance(OFFER_TTL);

        let repeat = handler(&world)
            .execute(&service(), input("courier-1"))
            .expect("repeat");

        assert_eq!(repeat.offer_id, DeliveryOfferId::new("generated-1"));
        assert!(!repeat.created);
        assert_eq!(world.offers_of("delivery-1").expect("read").len(), 1);
    }

    #[test]
    fn expired_offer_is_replaced_by_a_new_one() {
        let world = World::new();
        world.waiting_delivery("delivery-1");
        world.courier("courier-1", CourierStatus::Available);
        handler(&world)
            .execute(&service(), input("courier-1"))
            .expect("first");
        world.clock.advance(OFFER_TTL + 1);

        let next = handler(&world)
            .execute(&service(), input("courier-1"))
            .expect("next");
        assert_eq!(next.offer_id, DeliveryOfferId::new("generated-2"));
        assert!(next.created);
    }

    /// AC-322
    #[test]
    fn busy_or_unavailable_courier_gets_no_offer() {
        let world = World::new();
        world.waiting_delivery("delivery-1");
        world.courier("busy", CourierStatus::Busy);
        world.courier("off", CourierStatus::Unavailable);

        assert_eq!(
            handler(&world).execute(&service(), input("busy")),
            Err(DeliveryApplicationError::CourierPrecondition(
                CourierError::AlreadyBusy
            ))
        );
        assert_eq!(
            handler(&world).execute(&service(), input("off")),
            Err(DeliveryApplicationError::CourierPrecondition(
                CourierError::Unavailable
            ))
        );
        assert!(world.offers_of("delivery-1").expect("read").is_empty());
    }

    #[test]
    fn assigned_delivery_is_not_offered() {
        let world = World::new();
        world.assigned_delivery("delivery-1", "courier-1");
        world.courier("courier-2", CourierStatus::Available);

        assert_eq!(
            handler(&world).execute(&service(), input("courier-2")),
            Err(DeliveryApplicationError::DeliveryPrecondition(
                DeliveryError::Transition(TransitionError::AlreadyInState {
                    state: State::Assigned
                })
            ))
        );
    }

    #[test]
    fn denied_subject_creates_no_offer() {
        let world = World::with_authorization(StaticAuthorization::deny_all());
        world.waiting_delivery("delivery-1");
        world.courier("courier-1", CourierStatus::Available);
        assert_eq!(
            handler(&world).execute(&service(), input("courier-1")),
            Err(DeliveryApplicationError::PermissionDenied)
        );
    }
}

//! Reject Delivery Offer: the addressee closes a current offer as rejected.
//! No Courier Assignment is created and no new search is started. The offer
//! and its `DeliveryOfferRejected` fact are written in one Unit of Work; a
//! repeated rejection writes nothing.

use crate::pkg::application::{Clock, Subject, UnitOfWork};
use crate::slices::delivery::domains::delivery::{
    DeliveryId, DeliveryOfferId, DeliveryOfferRepository, OfferState,
};

use super::authorization::{DeliveryAuthorization, DeliveryPermission, DeliveryResource};
use super::error::DeliveryApplicationError;
use super::outbox::{DeliveryOutbox, publish};
use super::{courier_id, load_offer, offer_id};

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct Input {
    pub offer_id: String,
    pub courier_id: String,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct Output {
    pub offer_id: DeliveryOfferId,
    pub delivery_id: DeliveryId,
    /// `false` when the same courier had already rejected this offer.
    pub changed: bool,
}

pub struct Handler<'a, U: UnitOfWork> {
    pub authorization: &'a dyn DeliveryAuthorization,
    pub unit_of_work: &'a U,
    pub offers: &'a dyn DeliveryOfferRepository,
    pub outbox: &'a dyn DeliveryOutbox,
    pub clock: &'a dyn Clock,
}

impl<U: UnitOfWork> Handler<'_, U> {
    pub fn execute(
        &self,
        subject: &Subject,
        input: Input,
    ) -> Result<Output, DeliveryApplicationError> {
        let offer_id = offer_id(&input.offer_id)?;
        let courier_id = courier_id(&input.courier_id)?;
        self.authorization.check(
            subject,
            DeliveryPermission::RejectDeliveryOffer,
            &DeliveryResource::Courier(courier_id.clone()),
        )?;

        let mut offer = load_offer(self.offers, &offer_id)?;
        let changed = if offer.state() == OfferState::Rejected && offer.courier_id() == &courier_id
        {
            false
        } else {
            offer.reject(&courier_id, self.clock.now_epoch_seconds())?;
            self.unit_of_work.run(|| {
                let rejected = offer.pull_events();
                self.offers.save(&offer)?;
                publish(self.outbox, rejected)?;
                Ok::<(), DeliveryApplicationError>(())
            })?;
            true
        };
        Ok(Output {
            offer_id,
            delivery_id: offer.delivery_id().clone(),
            changed,
        })
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::infrastructure::memory::{MemoryStore, StaticAuthorization};
    use crate::slices::courier::domains::courier::CourierId;
    use crate::slices::courier::domains::courier::CourierStatus;
    use crate::slices::delivery::applications::outbox::OutboxMessage;
    use crate::slices::delivery::applications::test_support::{NOW, OFFER_TTL, World, subject};
    use crate::slices::delivery::domains::delivery::{DeliveryOfferError, DomainEvent, State};

    fn rejection_fact() -> OutboxMessage {
        OutboxMessage::delivery(DomainEvent::DeliveryOfferRejected {
            delivery_id: DeliveryId::new("delivery-1"),
            offer_id: DeliveryOfferId::new("offer-1"),
            courier_id: CourierId::new("courier-1"),
        })
    }

    fn handler(world: &World) -> Handler<'_, MemoryStore> {
        Handler {
            authorization: &world.authorization,
            unit_of_work: &world.store,
            offers: &world.store,
            outbox: &world.store,
            clock: &world.clock,
        }
    }

    fn input(courier: &str) -> Input {
        Input {
            offer_id: "offer-1".to_owned(),
            courier_id: courier.to_owned(),
        }
    }

    fn world() -> World {
        let world = World::new();
        world.waiting_delivery("delivery-1");
        world.courier("courier-1", CourierStatus::Available);
        world.offer("offer-1", "delivery-1", "courier-1");
        world
    }

    /// AC-329
    #[test]
    fn addressee_rejects_offer_without_assignment() {
        let world = world();

        let output = handler(&world)
            .execute(&subject("courier-1"), input("courier-1"))
            .expect("rejected");

        assert_eq!(
            output,
            Output {
                offer_id: DeliveryOfferId::new("offer-1"),
                delivery_id: DeliveryId::new("delivery-1"),
                changed: true,
            }
        );
        assert_eq!(world.offer_state("offer-1"), OfferState::Rejected);
        assert_eq!(world.delivery_state("delivery-1"), State::Waiting);
        assert_eq!(world.courier_status("courier-1"), CourierStatus::Available);
        assert_eq!(world.store.outbox_messages(), vec![rejection_fact()]);
        assert_eq!(world.store.commits(), 1);
    }

    /// AC-330
    #[test]
    fn repeated_rejection_is_a_no_op() {
        let world = world();
        handler(&world)
            .execute(&subject("courier-1"), input("courier-1"))
            .expect("first");

        let repeat = handler(&world)
            .execute(&subject("courier-1"), input("courier-1"))
            .expect("repeat");
        assert!(!repeat.changed);
        assert_eq!(world.offer_state("offer-1"), OfferState::Rejected);
        assert_eq!(world.store.outbox_messages(), vec![rejection_fact()]);
    }

    #[test]
    fn expired_or_foreign_offer_cannot_be_rejected() {
        let world = world();
        assert_eq!(
            handler(&world).execute(&subject("courier-2"), input("courier-2")),
            Err(DeliveryApplicationError::OfferPrecondition(
                DeliveryOfferError::NotAddressedToCourier
            ))
        );
        world.clock.set(NOW + OFFER_TTL + 1);
        assert_eq!(
            handler(&world).execute(&subject("courier-1"), input("courier-1")),
            Err(DeliveryApplicationError::OfferPrecondition(
                DeliveryOfferError::Expired
            ))
        );
        assert_eq!(world.offer_state("offer-1"), OfferState::Pending);
        assert!(world.store.outbox_messages().is_empty());
    }

    #[test]
    fn denied_subject_cannot_reject() {
        let world = World::with_authorization(StaticAuthorization::deny_all());
        world.waiting_delivery("delivery-1");
        world.offer("offer-1", "delivery-1", "courier-1");
        assert_eq!(
            handler(&world).execute(&subject("intruder"), input("courier-1")),
            Err(DeliveryApplicationError::PermissionDenied)
        );
        assert_eq!(world.offer_state("offer-1"), OfferState::Pending);
    }
}

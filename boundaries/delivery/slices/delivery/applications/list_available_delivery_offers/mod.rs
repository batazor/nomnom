//! List Available Delivery Offers: a courier reads its pending, not yet
//! expired Delivery Offers without changing state.

use crate::pkg::application::{Clock, Subject};
use crate::pkg::persistence::RepositoryError;
use crate::slices::courier::domains::courier::CourierId;
use crate::slices::delivery::domains::delivery::{Address, DeliveryId, DeliveryOfferId};

use super::authorization::{DeliveryAuthorization, DeliveryPermission, DeliveryResource};
use super::courier_id;
use super::error::DeliveryApplicationError;

/// Courier-facing representation of a pending Delivery Offer.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct AvailableDeliveryOfferView {
    pub offer_id: DeliveryOfferId,
    pub delivery_id: DeliveryId,
    pub pickup_address: Address,
    pub dropoff_address: Address,
    pub expires_at_epoch_seconds: i64,
}

/// Query model of pending offers addressed to a courier. Expiry is applied
/// by the use case with the current time.
pub trait PendingDeliveryOffersReadModel {
    fn pending_offers_for(
        &self,
        courier_id: &CourierId,
    ) -> Result<Vec<AvailableDeliveryOfferView>, RepositoryError>;
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct Input {
    pub courier_id: String,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct Output {
    pub courier_id: CourierId,
    pub offers: Vec<AvailableDeliveryOfferView>,
}

pub struct Handler<'a> {
    pub authorization: &'a dyn DeliveryAuthorization,
    pub pending_offers: &'a dyn PendingDeliveryOffersReadModel,
    pub clock: &'a dyn Clock,
}

impl Handler<'_> {
    pub fn execute(
        &self,
        subject: &Subject,
        input: Input,
    ) -> Result<Output, DeliveryApplicationError> {
        let courier_id = courier_id(&input.courier_id)?;
        self.authorization.check(
            subject,
            DeliveryPermission::ListAvailableDeliveryOffers,
            &DeliveryResource::Courier(courier_id.clone()),
        )?;

        let now = self.clock.now_epoch_seconds();
        let offers = self
            .pending_offers
            .pending_offers_for(&courier_id)?
            .into_iter()
            .filter(|offer| now <= offer.expires_at_epoch_seconds)
            .collect();
        Ok(Output { courier_id, offers })
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::infrastructure::memory::StaticAuthorization;
    use crate::slices::courier::domains::courier::CourierStatus;
    use crate::slices::delivery::applications::test_support::{NOW, OFFER_TTL, World, subject};
    use crate::slices::delivery::domains::delivery::{DeliveryOfferRepository, OfferState};

    fn handler(world: &World) -> Handler<'_> {
        Handler {
            authorization: &world.authorization,
            pending_offers: &world.store,
            clock: &world.clock,
        }
    }

    fn input() -> Input {
        Input {
            courier_id: "courier-1".to_owned(),
        }
    }

    /// AC-333
    #[test]
    fn lists_open_offers_with_addresses_and_deadline() {
        let world = World::new();
        world.courier("courier-1", CourierStatus::Available);
        world.waiting_delivery("delivery-1");
        world.offer("offer-1", "delivery-1", "courier-1");
        world.clock.set(NOW + OFFER_TTL);

        let output = handler(&world)
            .execute(&subject("courier-1"), input())
            .expect("query");

        assert_eq!(
            output.offers,
            vec![AvailableDeliveryOfferView {
                offer_id: DeliveryOfferId::new("offer-1"),
                delivery_id: DeliveryId::new("delivery-1"),
                pickup_address: Address::new("Restaurant street 1").expect("pickup"),
                dropoff_address: Address::new("Customer street 2").expect("dropoff"),
                expires_at_epoch_seconds: NOW + OFFER_TTL,
            }]
        );
        assert_eq!(world.offer_state("offer-1"), OfferState::Pending);
    }

    /// AC-334
    #[test]
    fn expired_closed_and_foreign_offers_are_not_listed() {
        let world = World::new();
        world.waiting_delivery("delivery-1");
        world.waiting_delivery("delivery-2");
        world.waiting_delivery("delivery-3");
        world.offer("expired", "delivery-1", "courier-1");
        world.offer("rejected", "delivery-2", "courier-1");
        world.offer("foreign", "delivery-3", "courier-2");
        let mut rejected =
            DeliveryOfferRepository::get(&world.store, &DeliveryOfferId::new("rejected"))
                .expect("read")
                .expect("offer");
        rejected
            .reject(&CourierId::new("courier-1"), NOW)
            .expect("reject");
        DeliveryOfferRepository::save(&world.store, &rejected).expect("save");
        world.clock.set(NOW + OFFER_TTL + 1);

        let output = handler(&world)
            .execute(&subject("courier-1"), input())
            .expect("query");

        assert!(output.offers.is_empty());
    }

    #[test]
    fn denied_subject_cannot_list_courier_offers() {
        let world = World::with_authorization(StaticAuthorization::deny_all());
        assert_eq!(
            handler(&world).execute(&subject("courier-2"), input()),
            Err(DeliveryApplicationError::PermissionDenied)
        );
    }
}

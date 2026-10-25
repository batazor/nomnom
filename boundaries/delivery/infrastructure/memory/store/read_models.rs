//! Query read models of [`MemoryStore`].

use crate::pkg::persistence::RepositoryError;
use crate::slices::courier::domains::courier::CourierId;
use crate::slices::delivery::applications::get_courier_current_delivery::{
    CourierCurrentDeliveryReadModel, CurrentDeliveryView,
};
use crate::slices::delivery::applications::list_available_delivery_offers::{
    AvailableDeliveryOfferView, PendingDeliveryOffersReadModel,
};
use crate::slices::delivery::domains::delivery::{OfferState, State};

use super::MemoryStore;

impl CourierCurrentDeliveryReadModel for MemoryStore {
    fn active_delivery_for(
        &self,
        courier_id: &CourierId,
    ) -> Result<Option<CurrentDeliveryView>, RepositoryError> {
        Ok(self
            .available()?
            .deliveries
            .values()
            .find(|record| {
                record.courier_id.as_ref() == Some(courier_id)
                    && matches!(record.state, State::Assigned | State::PickedUp)
            })
            .map(|record| CurrentDeliveryView {
                delivery_id: record.id.clone(),
                order_id: record.order_id.clone(),
                pickup_address: record.pickup_address.clone(),
                dropoff_address: record.dropoff_address.clone(),
                state: record.state,
            }))
    }
}

impl PendingDeliveryOffersReadModel for MemoryStore {
    fn pending_offers_for(
        &self,
        courier_id: &CourierId,
    ) -> Result<Vec<AvailableDeliveryOfferView>, RepositoryError> {
        let state = self.available()?;
        Ok(state
            .offers
            .iter()
            .filter(|offer| {
                offer.courier_id() == courier_id && offer.state() == OfferState::Pending
            })
            .filter_map(|offer| {
                state
                    .deliveries
                    .get(offer.delivery_id().as_str())
                    .map(|delivery| AvailableDeliveryOfferView {
                        offer_id: offer.id().clone(),
                        delivery_id: offer.delivery_id().clone(),
                        pickup_address: delivery.pickup_address.clone(),
                        dropoff_address: delivery.dropoff_address.clone(),
                        expires_at_epoch_seconds: offer.expires_at_epoch_seconds(),
                    })
            })
            .collect())
    }
}

//! Offers of one Delivery: the query port of commands that process every
//! offer of a delivery (Accept Delivery Offer withdraws the others, Offer
//! Delivery and Find Courier skip couriers already offered). The query returns
//! ids only; the command loads each aggregate through
//! [`DeliveryOfferRepository::get`] and saves it there.

use crate::pkg::persistence::RepositoryError;
use crate::slices::delivery::domains::delivery::{
    DeliveryId, DeliveryOffer, DeliveryOfferId, DeliveryOfferRepository,
};

pub trait OffersOfDelivery {
    /// Ids of all offers of the delivery in any state, ordered by creation.
    fn offer_ids_of(
        &self,
        delivery_id: &DeliveryId,
    ) -> Result<Vec<DeliveryOfferId>, RepositoryError>;
}

/// Loads the offers of the delivery through the repository, in query order.
pub(crate) fn load_offers_of(
    query: &dyn OffersOfDelivery,
    offers: &dyn DeliveryOfferRepository,
    delivery_id: &DeliveryId,
) -> Result<Vec<DeliveryOffer>, RepositoryError> {
    let mut loaded = Vec::new();
    for id in query.offer_ids_of(delivery_id)? {
        if let Some(offer) = offers.get(&id)? {
            loaded.push(offer);
        }
    }
    Ok(loaded)
}

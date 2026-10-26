//! Порт чтения authorization revision ресурса (FR-447, FR-448): ZedToken
//! последней записи relationship, сохранённый вместе с `Courier` или
//! `Delivery`. SpiceDB-адаптер проверяет такой ресурс с `at_least_as_fresh`.

use std::sync::Arc;

use super::relationships::{COURIER, DELIVERY};
use crate::pkg::persistence::AuthorizationRevision;
use crate::slices::courier::domains::courier::{CourierId, CourierRepository};
use crate::slices::delivery::domains::delivery::{DeliveryId, DeliveryRepository};

/// Revision ресурса `resource_type:resource_id`; `Ok(None)` — ресурса нет или
/// revision не сохранена, `Err` — хранилище не ответило.
pub trait RevisionReader: Send + Sync {
    fn revision(
        &self,
        resource_type: &str,
        resource_id: &str,
    ) -> Result<Option<AuthorizationRevision>, String>;
}

/// [`RevisionReader`] поверх репозиториев `Courier` и `Delivery`.
pub struct RepositoryRevisions<S: ?Sized>(pub Arc<S>);

impl<S> RevisionReader for RepositoryRevisions<S>
where
    S: ?Sized + CourierRepository + DeliveryRepository + Send + Sync,
{
    fn revision(
        &self,
        resource_type: &str,
        resource_id: &str,
    ) -> Result<Option<AuthorizationRevision>, String> {
        match resource_type {
            COURIER => CourierRepository::get(&*self.0, &CourierId::new(resource_id))
                .map(|courier| {
                    courier.and_then(|courier| courier.authorization_revision().cloned())
                })
                .map_err(|error| error.to_string()),
            DELIVERY => DeliveryRepository::get(&*self.0, &DeliveryId::new(resource_id))
                .map(|delivery| {
                    delivery.and_then(|delivery| delivery.authorization_revision().cloned())
                })
                .map_err(|error| error.to_string()),
            _ => Ok(None),
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::infrastructure::repository::test_support::TestStore;
    use crate::slices::courier::domains::courier::MarketId;
    use crate::slices::courier::domains::courier::{Courier, CourierName};
    use crate::slices::delivery::domains::delivery::{Delivery, OrderId};

    #[test]
    fn reads_revisions_stored_with_courier_and_delivery() {
        let store = Arc::new(TestStore::new());
        let mut courier = Courier::new(
            CourierId::new("courier-1"),
            CourierName::new("Timur").expect("courier name"),
            MarketId::new("berlin").expect("market id"),
        );
        courier.record_authorization_revision(AuthorizationRevision::new("zed-c"));
        CourierRepository::add(&*store, &courier).expect("courier");
        let mut delivery = Delivery::new(
            DeliveryId::new("delivery-1"),
            OrderId::new("order-1"),
            MarketId::new("berlin").expect("market id"),
        );
        delivery.record_authorization_revision(AuthorizationRevision::new("zed-d"));
        DeliveryRepository::add(&*store, &delivery).expect("delivery");
        let revisions = RepositoryRevisions(Arc::clone(&store));

        assert_eq!(
            revisions.revision(COURIER, "courier-1"),
            Ok(Some(AuthorizationRevision::new("zed-c")))
        );
        assert_eq!(
            revisions.revision(DELIVERY, "delivery-1"),
            Ok(Some(AuthorizationRevision::new("zed-d")))
        );
        assert_eq!(revisions.revision(DELIVERY, "missing"), Ok(None));
        assert_eq!(revisions.revision("platform", "nomnom"), Ok(None));

        store.set_unavailable(true);
        assert!(revisions.revision(COURIER, "courier-1").is_err());
    }
}

//! Create Delivery: the fulfillment process of `orders` requests delivery of
//! an order. Idempotent by `OrderId`; does not start the courier search.

use crate::pkg::application::{IdGenerator, Subject};
use crate::pkg::persistence::RepositoryError;
use crate::slices::delivery::domains::delivery::{
    Address, Delivery, DeliveryId, DeliveryRepository, OrderId,
};

use super::authorization::{DeliveryAuthorization, DeliveryPermission, DeliveryResource};
use super::error::DeliveryApplicationError;
use super::order_id;

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct Input {
    pub order_id: String,
    pub pickup_address: String,
    pub dropoff_address: String,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct Output {
    pub delivery_id: DeliveryId,
    /// `false` when an identical earlier request already created it.
    pub created: bool,
}

pub struct Handler<'a> {
    pub authorization: &'a dyn DeliveryAuthorization,
    pub deliveries: &'a dyn DeliveryRepository,
    pub ids: &'a dyn IdGenerator,
}

impl Handler<'_> {
    pub fn execute(
        &self,
        subject: &Subject,
        input: Input,
    ) -> Result<Output, DeliveryApplicationError> {
        let order_id = order_id(&input.order_id)?;
        let pickup_address = Address::new(&input.pickup_address)?;
        let dropoff_address = Address::new(&input.dropoff_address)?;
        self.authorization.check(
            subject,
            DeliveryPermission::CreateDelivery,
            &DeliveryResource::Platform,
        )?;

        if let Some(existing) = self.deliveries.find_by_order(&order_id)? {
            return replay(&existing, &pickup_address, &dropoff_address);
        }
        let delivery = Delivery::new(
            DeliveryId::new(self.ids.next_id()),
            order_id.clone(),
            pickup_address.clone(),
            dropoff_address.clone(),
        );
        match self.deliveries.add(&delivery) {
            Ok(()) => Ok(Output {
                delivery_id: delivery.id().clone(),
                created: true,
            }),
            Err(RepositoryError::Conflict) => {
                let existing = self.concurrent_delivery(&order_id)?;
                replay(&existing, &pickup_address, &dropoff_address)
            }
            Err(error) => Err(error.into()),
        }
    }

    /// Resolves a lost insert race against a concurrent identical request.
    fn concurrent_delivery(
        &self,
        order_id: &OrderId,
    ) -> Result<Delivery, DeliveryApplicationError> {
        self.deliveries.find_by_order(order_id)?.ok_or_else(|| {
            DeliveryApplicationError::Internal(
                "delivery insert conflicted without an existing delivery".to_owned(),
            )
        })
    }
}

fn replay(
    existing: &Delivery,
    pickup_address: &Address,
    dropoff_address: &Address,
) -> Result<Output, DeliveryApplicationError> {
    if existing.pickup_address() == pickup_address && existing.dropoff_address() == dropoff_address
    {
        Ok(Output {
            delivery_id: existing.id().clone(),
            created: false,
        })
    } else {
        Err(DeliveryApplicationError::DeliveryAlreadyExists(
            existing.id().clone(),
        ))
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::infrastructure::memory::StaticAuthorization;
    use crate::slices::delivery::applications::error::InvalidDeliveryRequest;
    use crate::slices::delivery::applications::test_support::{World, service};
    use crate::slices::delivery::domains::delivery::{DeliveryValidationError, State};

    fn input(pickup: &str) -> Input {
        Input {
            order_id: "order-1".to_owned(),
            pickup_address: pickup.to_owned(),
            dropoff_address: "Customer street 2".to_owned(),
        }
    }

    fn handler(world: &World) -> Handler<'_> {
        Handler {
            authorization: &world.authorization,
            deliveries: &world.store,
            ids: &world.ids,
        }
    }

    /// AC-313
    #[test]
    fn creates_waiting_delivery_without_courier() {
        let world = World::new();

        let output = handler(&world)
            .execute(&service(), input(" Restaurant   street 1 "))
            .expect("delivery created");

        assert_eq!(
            output,
            Output {
                delivery_id: DeliveryId::new("generated-1"),
                created: true,
            }
        );
        let delivery = world.delivery("generated-1");
        assert_eq!(delivery.state(), State::Waiting);
        assert_eq!(delivery.courier_id(), None);
        assert_eq!(delivery.order_id(), &OrderId::new("order-1"));
        assert_eq!(delivery.pickup_address().as_str(), "Restaurant street 1");
        assert!(world.store.outbox_messages().is_empty());
    }

    /// AC-314
    #[test]
    fn identical_repeat_returns_existing_delivery() {
        let world = World::new();
        handler(&world)
            .execute(&service(), input("Restaurant street 1"))
            .expect("first");

        let repeat = handler(&world)
            .execute(&service(), input("Restaurant  street 1"))
            .expect("repeat");

        assert_eq!(
            repeat,
            Output {
                delivery_id: DeliveryId::new("generated-1"),
                created: false,
            }
        );
        assert_eq!(
            DeliveryRepository::find_by_order(&world.store, &OrderId::new("order-1"))
                .expect("read")
                .map(|delivery| delivery.id().clone()),
            Some(DeliveryId::new("generated-1"))
        );
    }

    /// AC-315
    #[test]
    fn conflicting_repeat_is_already_exists() {
        let world = World::new();
        handler(&world)
            .execute(&service(), input("Restaurant street 1"))
            .expect("first");

        assert_eq!(
            handler(&world).execute(&service(), input("Other street 9")),
            Err(DeliveryApplicationError::DeliveryAlreadyExists(
                DeliveryId::new("generated-1")
            ))
        );
        assert_eq!(
            world.delivery("generated-1").pickup_address().as_str(),
            "Restaurant street 1"
        );
    }

    /// AC-316
    #[test]
    fn empty_address_is_invalid() {
        let world = World::new();
        assert_eq!(
            handler(&world).execute(&service(), input("  ")),
            Err(DeliveryApplicationError::InvalidRequest(
                InvalidDeliveryRequest::Validation(DeliveryValidationError::EmptyAddress)
            ))
        );
        assert_eq!(
            DeliveryRepository::find_by_order(&world.store, &OrderId::new("order-1")),
            Ok(None)
        );
    }

    #[test]
    fn denied_subject_creates_nothing() {
        let world = World::with_authorization(StaticAuthorization::deny_all());
        assert_eq!(
            handler(&world).execute(&service(), input("Restaurant street 1")),
            Err(DeliveryApplicationError::PermissionDenied)
        );
        assert_eq!(
            DeliveryRepository::find_by_order(&world.store, &OrderId::new("order-1")),
            Ok(None)
        );
    }
}

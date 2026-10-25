//! Get Courier Current Delivery: a courier reads its active Delivery —
//! assigned or picked up — or the explicit absence of one. The query reads the
//! `Delivery` model, so it belongs to the `delivery` slice; the courier's right
//! is checked by the `courier` slice authorization port as before.

use crate::pkg::application::Subject;
use crate::pkg::persistence::RepositoryError;
use crate::slices::courier::applications::assignment::CourierAssignment;
use crate::slices::courier::applications::authorization::{
    CourierAuthorization, CourierPermission, CourierResource,
};
use crate::slices::courier::domains::courier::CourierId;
use crate::slices::delivery::domains::delivery::{Address, DeliveryId, OrderId, State};

use super::courier_id;
use super::error::DeliveryApplicationError;

/// Courier-facing representation of the active Delivery.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct CurrentDeliveryView {
    pub delivery_id: DeliveryId,
    pub order_id: OrderId,
    pub pickup_address: Address,
    pub dropoff_address: Address,
    pub state: State,
}

/// Query model of the delivery a courier is currently assigned to.
pub trait CourierCurrentDeliveryReadModel {
    fn active_delivery_for(
        &self,
        courier_id: &CourierId,
    ) -> Result<Option<CurrentDeliveryView>, RepositoryError>;
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct Input {
    pub courier_id: String,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct Output {
    pub courier_id: CourierId,
    /// `None` when the courier has no assigned or picked up Delivery.
    pub current_delivery: Option<CurrentDeliveryView>,
}

pub struct Handler<'a> {
    pub authorization: &'a dyn CourierAuthorization,
    pub couriers: &'a dyn CourierAssignment,
    pub current_deliveries: &'a dyn CourierCurrentDeliveryReadModel,
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
            CourierPermission::ViewCourierCurrentDelivery,
            &CourierResource::Courier(courier_id.clone()),
        )?;

        self.couriers.ensure_registered(&courier_id)?;
        let current_delivery = self.current_deliveries.active_delivery_for(&courier_id)?;
        Ok(Output {
            courier_id,
            current_delivery,
        })
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::infrastructure::memory::StaticAuthorization;
    use crate::infrastructure::postgres::test_support::TestStore;
    use crate::slices::courier::domains::courier::{Courier, CourierRepository, CourierStatus};
    use crate::slices::delivery::domains::delivery::{Delivery, DeliveryRepository};

    fn subject() -> Subject {
        Subject::User("courier-1".to_owned())
    }

    fn input() -> Input {
        Input {
            courier_id: "courier-1".to_owned(),
        }
    }

    fn delivery(id: &str, order: &str) -> Delivery {
        Delivery::new(
            DeliveryId::new(id),
            OrderId::new(order),
            Address::new("Restaurant street 1").expect("pickup"),
            Address::new("Customer street 2").expect("dropoff"),
        )
    }

    fn store_with_courier() -> TestStore {
        let store = TestStore::new();
        CourierRepository::add(
            &store,
            &Courier::restore(CourierId::new("courier-1"), CourierStatus::Busy),
        )
        .expect("seed courier");
        store
    }

    /// AC-310
    #[test]
    fn returns_assigned_delivery_of_the_courier() {
        let store = store_with_courier();
        let mut assigned = delivery("delivery-1", "order-1");
        assigned
            .assign(CourierId::new("courier-1"))
            .expect("assign");
        DeliveryRepository::add(&store, &assigned).expect("seed delivery");
        DeliveryRepository::add(&store, &delivery("delivery-2", "order-2")).expect("seed");
        let authorization = StaticAuthorization::allow_all();
        let handler = Handler {
            authorization: &authorization,
            couriers: &store,
            current_deliveries: &store,
        };

        let output = handler.execute(&subject(), input()).expect("query");

        assert_eq!(
            output.current_delivery,
            Some(CurrentDeliveryView {
                delivery_id: DeliveryId::new("delivery-1"),
                order_id: OrderId::new("order-1"),
                pickup_address: Address::new("Restaurant street 1").expect("pickup"),
                dropoff_address: Address::new("Customer street 2").expect("dropoff"),
                state: State::Assigned,
            })
        );
    }

    /// AC-311
    #[test]
    fn completed_delivery_is_not_current() {
        let store = store_with_courier();
        let courier = CourierId::new("courier-1");
        let mut completed = delivery("delivery-1", "order-1");
        completed.assign(courier.clone()).expect("assign");
        completed.confirm_pickup(&courier).expect("pickup");
        completed.complete(&courier).expect("complete");
        DeliveryRepository::add(&store, &completed).expect("seed delivery");
        let authorization = StaticAuthorization::allow_all();
        let handler = Handler {
            authorization: &authorization,
            couriers: &store,
            current_deliveries: &store,
        };

        assert_eq!(
            handler.execute(&subject(), input()),
            Ok(Output {
                courier_id: courier,
                current_delivery: None,
            })
        );
    }

    #[test]
    fn denied_subject_cannot_read_courier_delivery() {
        let store = store_with_courier();
        let authorization = StaticAuthorization::deny_all();
        let handler = Handler {
            authorization: &authorization,
            couriers: &store,
            current_deliveries: &store,
        };
        assert_eq!(
            handler.execute(&Subject::User("courier-2".to_owned()), input()),
            Err(DeliveryApplicationError::PermissionDenied)
        );
    }

    #[test]
    fn unknown_courier_is_not_found() {
        let store = TestStore::new();
        let authorization = StaticAuthorization::allow_all();
        let handler = Handler {
            authorization: &authorization,
            couriers: &store,
            current_deliveries: &store,
        };
        assert_eq!(
            handler.execute(&subject(), input()),
            Err(DeliveryApplicationError::CourierNotFound(CourierId::new(
                "courier-1"
            )))
        );
    }
}

//! Get Order Courier: whoever may view an order — its customer first of all —
//! reads the name of the courier delivering it (FR-626). The right is
//! `order:<id>#view`; delivery keeps the courier of a delivered order, so the
//! name stays after the handover.

use crate::pkg::application::Subject;
use crate::pkg::persistence::RepositoryError;
use crate::slices::courier::domains::courier::{CourierId, CourierName};
use crate::slices::delivery::domains::delivery::OrderId;

use super::authorization::{DeliveryAuthorization, DeliveryPermission, DeliveryResource};
use super::error::DeliveryApplicationError;
use super::order_id;

/// Courier of the Delivery of an order.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct OrderCourierView {
    pub courier_id: CourierId,
    pub name: CourierName,
}

/// Query model of the courier of an order.
pub trait OrderCourierReadModel {
    /// Courier of the Delivery of `order_id` while it is assigned, picked up
    /// or delivered; `None` before an Assignment and after a failure or a
    /// cancellation.
    fn courier_of_order(
        &self,
        order_id: &OrderId,
    ) -> Result<Option<OrderCourierView>, RepositoryError>;
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct Input {
    pub order_id: String,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct Output {
    pub order_id: OrderId,
    pub courier: Option<OrderCourierView>,
}

pub struct Handler<'a> {
    pub authorization: &'a dyn DeliveryAuthorization,
    pub couriers: &'a dyn OrderCourierReadModel,
}

impl Handler<'_> {
    pub fn execute(
        &self,
        subject: &Subject,
        input: Input,
    ) -> Result<Output, DeliveryApplicationError> {
        let order_id = order_id(&input.order_id)?;
        self.authorization.check(
            subject,
            DeliveryPermission::ViewOrderCourier,
            &DeliveryResource::Order(order_id.clone()),
        )?;
        let courier = self.couriers.courier_of_order(&order_id)?;
        Ok(Output { order_id, courier })
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::infrastructure::memory::StaticAuthorization;
    use crate::slices::delivery::applications::test_support::World;

    fn customer() -> Subject {
        Subject::User("customer-1".to_owned())
    }

    fn input(order: &str) -> Input {
        Input {
            order_id: order.to_owned(),
        }
    }

    /// AC-795
    #[test]
    fn customer_reads_the_courier_of_the_order() {
        let world = World::new();
        world.assigned_delivery("delivery-1", "courier-1");
        let order = world.delivery("delivery-1").order_id().clone();
        let handler = Handler {
            authorization: &world.authorization,
            couriers: &world.store,
        };

        let output = handler
            .execute(&customer(), input(order.as_str()))
            .expect("courier of the order");

        let courier = output.courier.expect("assigned courier");
        assert_eq!(courier.courier_id, CourierId::new("courier-1"));
        assert_eq!(
            world.authorization.delivery_checks(),
            vec![(
                customer(),
                DeliveryPermission::ViewOrderCourier,
                DeliveryResource::Order(order)
            )]
        );
    }

    #[test]
    fn waiting_delivery_has_no_courier() {
        let world = World::new();
        world.waiting_delivery("delivery-1");
        let order = world.delivery("delivery-1").order_id().clone();
        let handler = Handler {
            authorization: &world.authorization,
            couriers: &world.store,
        };

        let output = handler
            .execute(&customer(), input(order.as_str()))
            .expect("no courier yet");

        assert_eq!(output.courier, None);
    }

    #[test]
    fn subject_who_may_not_view_the_order_reads_nothing() {
        let world = World::with_authorization(StaticAuthorization::deny_all());
        let handler = Handler {
            authorization: &world.authorization,
            couriers: &world.store,
        };

        assert_eq!(
            handler.execute(&customer(), input("order-1")),
            Err(DeliveryApplicationError::PermissionDenied)
        );
    }
}

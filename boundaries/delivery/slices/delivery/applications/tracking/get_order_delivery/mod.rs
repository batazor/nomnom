//! Get Order Delivery: whoever may view an order — its customer first of all —
//! reads the delivery of the order in one query: the name of its courier
//! (FR-626) and the live tracking (FR-660) — the Tracking Channel of the
//! position stream, the Virtual City, the pickup and dropoff points and the
//! stored courier position. The right is `order:<id>#view`, checked once.
//!
//! The courier stays after the handover: delivery keeps the courier of a
//! delivered order. The tracking exists only while the delivery is assigned
//! or picked up: the channel is a capability, handed out only here.

use crate::pkg::authz::Subject;
use crate::pkg::persistence::RepositoryError;
use crate::slices::courier::applications::update_courier_location::CourierPosition;
use crate::slices::courier::domains::courier::{CourierId, CourierName, Location};
use crate::slices::delivery::domains::delivery::{
    Intersection, OrderId, TrackingChannel, VirtualCity,
};

use super::super::authorization::{DeliveryAuthorization, DeliveryPermission, DeliveryResource};
use super::super::error::DeliveryApplicationError;
use super::super::order_id;

/// Courier of the Delivery of an order.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct OrderCourierView {
    pub courier_id: CourierId,
    pub name: CourierName,
}

/// Stored tracking of an active delivery.
#[derive(Clone, Debug, PartialEq)]
pub struct OrderTrackingRecord {
    pub channel: TrackingChannel,
    pub pickup: Intersection,
    pub dropoff: Intersection,
    /// Stored (thinned) position of the courier; `None` while unknown.
    pub courier_position: Option<CourierPosition>,
}

/// Stored delivery of an order as its viewer may see it.
#[derive(Clone, Debug, Default, PartialEq)]
pub struct OrderDeliveryRecord {
    /// Set while the delivery is assigned, picked up or delivered.
    pub courier: Option<OrderCourierView>,
    /// Set while the delivery is assigned or picked up and has a Tracking
    /// Channel.
    pub tracking: Option<OrderTrackingRecord>,
}

/// Query model of the delivery of an order.
pub trait OrderDeliveryReadModel {
    /// Delivery of `order_id`; without a Delivery both parts are empty.
    fn delivery_of_order(&self, order_id: &OrderId)
    -> Result<OrderDeliveryRecord, RepositoryError>;
}

/// Tracking of an order as its viewer sees it: points in coordinates of the
/// Virtual City.
#[derive(Clone, Debug, PartialEq)]
pub struct OrderTrackingView {
    pub channel: TrackingChannel,
    pub city: VirtualCity,
    pub pickup: Location,
    pub dropoff: Location,
    pub courier_position: Option<CourierPosition>,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct Input {
    pub order_id: String,
}

#[derive(Clone, Debug, PartialEq)]
pub struct Output {
    pub order_id: OrderId,
    pub courier: Option<OrderCourierView>,
    pub tracking: Option<OrderTrackingView>,
}

pub struct Handler<'a> {
    pub authorization: &'a dyn DeliveryAuthorization,
    pub deliveries: &'a dyn OrderDeliveryReadModel,
    pub city: &'a VirtualCity,
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
            DeliveryPermission::ViewOrderDelivery,
            &DeliveryResource::Order(order_id.clone()),
        )?;
        let record = self.deliveries.delivery_of_order(&order_id)?;
        let tracking = record
            .tracking
            .map(|tracking| self.view(tracking))
            .transpose()?;
        Ok(Output {
            order_id,
            courier: record.courier,
            tracking,
        })
    }

    fn view(
        &self,
        record: OrderTrackingRecord,
    ) -> Result<OrderTrackingView, DeliveryApplicationError> {
        let location = |at: Intersection| {
            self.city.location(at).map_err(|error| {
                DeliveryApplicationError::Internal(format!(
                    "tracking point outside the virtual city: {error}"
                ))
            })
        };
        Ok(OrderTrackingView {
            channel: record.channel,
            city: *self.city,
            pickup: location(record.pickup)?,
            dropoff: location(record.dropoff)?,
            courier_position: record.courier_position,
        })
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::infrastructure::memory::StaticAuthorization;
    use crate::slices::delivery::applications::test_support::{World, city, tracking};
    use crate::slices::delivery::domains::delivery::DeliveryRepository;

    fn customer() -> Subject {
        Subject::User("customer-1".to_owned())
    }

    fn input(order: &str) -> Input {
        Input {
            order_id: order.to_owned(),
        }
    }

    fn handler<'a>(world: &'a World, city: &'a VirtualCity) -> Handler<'a> {
        Handler {
            authorization: &world.authorization,
            deliveries: &world.store,
            city,
        }
    }

    /// AC-795, AC-874: one check of `order#view` reads the courier and the
    /// tracking of an assigned delivery.
    #[test]
    fn customer_reads_the_courier_and_tracking_of_an_assigned_delivery() {
        let world = World::new();
        world.tracked_delivery("delivery-1", "courier-1", false);
        world.courier_position("courier-1", 52.52, 13.40, 990);
        let city = city();

        let output = handler(&world, &city)
            .execute(&customer(), input("order-of-delivery-1"))
            .expect("delivery of the order");

        let courier = output.courier.expect("assigned courier");
        assert_eq!(courier.courier_id, CourierId::new("courier-1"));
        let tracking_view = output.tracking.expect("active delivery");
        assert_eq!(tracking_view.channel, tracking().channel);
        assert_eq!(tracking_view.city, city);
        assert_eq!(
            tracking_view.pickup,
            city.location(tracking().pickup).expect("pickup")
        );
        assert_eq!(
            tracking_view.dropoff,
            city.location(tracking().dropoff).expect("dropoff")
        );
        let position = tracking_view.courier_position.expect("courier position");
        assert_eq!(position.courier_id, CourierId::new("courier-1"));
        assert_eq!(position.recorded_at_epoch_seconds, 990);
        assert_eq!(
            world.authorization.delivery_checks(),
            vec![(
                customer(),
                DeliveryPermission::ViewOrderDelivery,
                DeliveryResource::Order(OrderId::new("order-of-delivery-1"))
            )]
        );
    }

    /// AC-875
    #[test]
    fn subject_who_may_not_view_the_order_reads_nothing() {
        let world = World::with_authorization(StaticAuthorization::deny_all());
        world.tracked_delivery("delivery-1", "courier-1", false);
        let city = city();

        assert_eq!(
            handler(&world, &city).execute(&customer(), input("order-of-delivery-1")),
            Err(DeliveryApplicationError::PermissionDenied)
        );
    }

    /// AC-876: a delivered order keeps its courier and has no tracking.
    #[test]
    fn delivered_order_keeps_the_courier_without_tracking() {
        let world = World::new();
        world.tracked_delivery("delivery-1", "courier-1", true);
        let mut delivery = world.delivery("delivery-1");
        delivery
            .complete(&CourierId::new("courier-1"))
            .expect("complete");
        DeliveryRepository::save(&world.store, &delivery).expect("save");
        let city = city();

        let output = handler(&world, &city)
            .execute(&customer(), input("order-of-delivery-1"))
            .expect("delivery of the order");

        assert!(output.courier.is_some());
        assert_eq!(output.tracking, None);
    }

    #[test]
    fn waiting_delivery_has_neither_courier_nor_tracking() {
        let world = World::new();
        world.waiting_delivery("delivery-1");
        let city = city();

        let output = handler(&world, &city)
            .execute(&customer(), input("order-of-delivery-1"))
            .expect("delivery of the order");

        assert_eq!(output.courier, None);
        assert_eq!(output.tracking, None);
    }

    #[test]
    fn delivery_without_channel_has_a_courier_but_no_tracking() {
        let world = World::new();
        world.assigned_delivery("delivery-1", "courier-1");
        let city = city();

        let output = handler(&world, &city)
            .execute(&customer(), input("order-of-delivery-1"))
            .expect("delivery of the order");

        assert!(output.courier.is_some());
        assert_eq!(output.tracking, None);
    }

    #[test]
    fn order_without_delivery_reads_empty() {
        let world = World::new();
        let city = city();

        let output = handler(&world, &city)
            .execute(&customer(), input("order-unknown"))
            .expect("empty delivery");

        assert_eq!(output.courier, None);
        assert_eq!(output.tracking, None);
    }
}

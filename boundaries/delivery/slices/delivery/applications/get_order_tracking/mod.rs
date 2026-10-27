//! Get Order Tracking: whoever may view an order — its customer first of all —
//! reads the live tracking of its delivery (FR-660): the Tracking Channel of
//! the position stream, the Virtual City, the pickup and dropoff points and
//! the stored courier position. The right is `order:<id>#view`; the channel
//! is a capability, so it is handed out only here and only while the
//! delivery is assigned or picked up.

use crate::pkg::application::Subject;
use crate::pkg::persistence::RepositoryError;
use crate::slices::courier::applications::update_courier_location::CourierPosition;
use crate::slices::courier::domains::courier::Location;
use crate::slices::delivery::domains::delivery::{
    Intersection, OrderId, TrackingChannel, VirtualCity,
};

use super::authorization::{DeliveryAuthorization, DeliveryPermission, DeliveryResource};
use super::error::DeliveryApplicationError;
use super::order_id;

/// Stored tracking of an active delivery.
#[derive(Clone, Debug, PartialEq)]
pub struct OrderTrackingRecord {
    pub channel: TrackingChannel,
    pub pickup: Intersection,
    pub dropoff: Intersection,
    /// Stored (thinned) position of the courier; `None` while unknown.
    pub courier_position: Option<CourierPosition>,
}

/// Query model of the tracking of an order.
pub trait OrderTrackingReadModel {
    /// Tracking of the Delivery of `order_id` while it is assigned or picked
    /// up and has a Tracking Channel; `None` otherwise.
    fn tracking_of_order(
        &self,
        order_id: &OrderId,
    ) -> Result<Option<OrderTrackingRecord>, RepositoryError>;
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
    pub tracking: Option<OrderTrackingView>,
}

pub struct Handler<'a> {
    pub authorization: &'a dyn DeliveryAuthorization,
    pub tracking: &'a dyn OrderTrackingReadModel,
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
            DeliveryPermission::ViewOrderTracking,
            &DeliveryResource::Order(order_id.clone()),
        )?;
        let tracking = self
            .tracking
            .tracking_of_order(&order_id)?
            .map(|record| self.view(record))
            .transpose()?;
        Ok(Output { order_id, tracking })
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
    use crate::slices::courier::domains::courier::CourierId;
    use crate::slices::delivery::applications::test_support::{World, city, tracking};
    use crate::slices::delivery::domains::delivery::{DeliveryRepository, OrderId};

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
            tracking: &world.store,
            city,
        }
    }

    /// AC-874
    #[test]
    fn customer_reads_the_tracking_of_an_assigned_delivery() {
        let world = World::new();
        world.tracked_delivery("delivery-1", "courier-1", false);
        world.courier_position("courier-1", 52.52, 13.40, 990);
        let city = city();

        let output = handler(&world, &city)
            .execute(&customer(), input("order-of-delivery-1"))
            .expect("tracking");

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
                DeliveryPermission::ViewOrderTracking,
                DeliveryResource::Order(OrderId::new("order-of-delivery-1"))
            )]
        );
    }

    /// AC-875
    #[test]
    fn subject_who_may_not_view_the_order_gets_no_channel() {
        let world = World::with_authorization(StaticAuthorization::deny_all());
        world.tracked_delivery("delivery-1", "courier-1", false);
        let city = city();

        assert_eq!(
            handler(&world, &city).execute(&customer(), input("order-of-delivery-1")),
            Err(DeliveryApplicationError::PermissionDenied)
        );
    }

    /// AC-876
    #[test]
    fn closed_delivery_has_no_tracking() {
        let world = World::new();
        world.tracked_delivery("delivery-1", "courier-1", true);
        let courier = CourierId::new("courier-1");
        let mut delivery = world.delivery("delivery-1");
        delivery.complete(&courier).expect("complete");
        DeliveryRepository::save(&world.store, &delivery).expect("save");
        let city = city();

        let output = handler(&world, &city)
            .execute(&customer(), input("order-of-delivery-1"))
            .expect("no tracking");

        assert_eq!(output.tracking, None);
    }

    #[test]
    fn waiting_delivery_and_delivery_without_channel_have_no_tracking() {
        let world = World::new();
        world.waiting_delivery("delivery-1");
        world.assigned_delivery("delivery-2", "courier-2");
        let city = city();

        for order in ["order-of-delivery-1", "order-of-delivery-2"] {
            let output = handler(&world, &city)
                .execute(&customer(), input(order))
                .expect("no tracking");
            assert_eq!(output.tracking, None, "{order}");
        }
    }
}

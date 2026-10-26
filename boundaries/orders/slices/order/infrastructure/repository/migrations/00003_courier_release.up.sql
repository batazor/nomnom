-- Record Courier Release (CourierUnassignedFromDelivery, DeliveryCancelled)
-- clears courier_id (and delivery_id for a cancelled Delivery) but keeps
-- courier_assigned_at as the time of the release, which orders later courier
-- events. A courier still requires its delivery and time; a delivery its time.
ALTER TABLE orders
    DROP CONSTRAINT orders_courier_assignment_complete,
    ADD CONSTRAINT orders_courier_assignment_complete CHECK (
        (courier_id IS NULL OR delivery_id IS NOT NULL)
        AND (delivery_id IS NULL OR courier_assigned_at IS NOT NULL)
    );

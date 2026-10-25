-- +goose Up
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

-- +goose Down
-- Released assignments have no representation in 00002.
UPDATE orders SET delivery_id = NULL, courier_id = NULL, courier_assigned_at = NULL
WHERE courier_id IS NULL;
ALTER TABLE orders
    DROP CONSTRAINT orders_courier_assignment_complete,
    ADD CONSTRAINT orders_courier_assignment_complete CHECK (
        (delivery_id IS NULL AND courier_id IS NULL AND courier_assigned_at IS NULL)
        OR (delivery_id IS NOT NULL AND courier_id IS NOT NULL AND courier_assigned_at IS NOT NULL)
    );

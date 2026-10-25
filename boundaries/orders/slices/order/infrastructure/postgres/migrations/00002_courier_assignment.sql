-- +goose Up
-- Courier Assignment of the Order as reported by delivery (CourierAssignedToDelivery).
-- It is the business source of the order#courier and order#delivery
-- relationships; reassignment replaces all three columns together.
ALTER TABLE orders
    ADD COLUMN delivery_id         TEXT,
    ADD COLUMN courier_id          TEXT,
    ADD COLUMN courier_assigned_at TIMESTAMPTZ,
    ADD CONSTRAINT orders_courier_assignment_complete CHECK (
        (delivery_id IS NULL AND courier_id IS NULL AND courier_assigned_at IS NULL)
        OR (delivery_id IS NOT NULL AND courier_id IS NOT NULL AND courier_assigned_at IS NOT NULL)
    );

-- +goose Down
ALTER TABLE orders
    DROP CONSTRAINT orders_courier_assignment_complete,
    DROP COLUMN courier_assigned_at,
    DROP COLUMN courier_id,
    DROP COLUMN delivery_id;

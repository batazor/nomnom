-- Delivery Window of a Scheduled Order (FR-681): the half-open interval
-- [delivery_window_start, delivery_window_end) reserved in restaurants at Place
-- Order and immutable. NULL in both columns is an ASAP Order, including every
-- Order placed before; no value is substituted.
ALTER TABLE orders
    ADD COLUMN delivery_window_start TIMESTAMPTZ,
    ADD COLUMN delivery_window_end   TIMESTAMPTZ,
    ADD CONSTRAINT orders_delivery_window_bounds CHECK (
        (delivery_window_start IS NULL AND delivery_window_end IS NULL)
        OR (delivery_window_start IS NOT NULL AND delivery_window_end IS NOT NULL
            AND delivery_window_start < delivery_window_end)
    );

-- Market of the Order (ADR 41): taken from the Restaurant at Place Order and
-- immutable. Orders placed before Markets belong to the launch Market berlin:
-- the column is added, existing rows are set to it explicitly once, then the
-- column becomes required. New rows always carry the Market of the Order; the
-- column has no default.
ALTER TABLE orders ADD COLUMN market_id TEXT;
UPDATE orders SET market_id = 'berlin' WHERE market_id IS NULL;
ALTER TABLE orders
    ALTER COLUMN market_id SET NOT NULL,
    ADD CONSTRAINT orders_market_id_format CHECK (market_id ~ '^[a-z][a-z0-9-]{1,31}$');

-- OrderDetails keeps the Market as a column: List Customer Orders returns only
-- Orders of the requested Market. Existing rows take it from their Order.
ALTER TABLE order_details ADD COLUMN market_id TEXT;
UPDATE order_details d SET market_id = o.market_id FROM orders o WHERE o.id = d.order_id;
ALTER TABLE order_details ALTER COLUMN market_id SET NOT NULL;

-- The history is read per (customer, Market) with the keyset cursor
-- (placed_at desc, order_id desc).
DROP INDEX order_details_customer_history;
CREATE INDEX order_details_customer_market_history
    ON order_details (customer_id, market_id, placed_at DESC, order_id DESC);

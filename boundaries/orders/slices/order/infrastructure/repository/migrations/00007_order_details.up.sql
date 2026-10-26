-- Menu Item name at Place Order, kept like unit_price. NULL — the Order was
-- placed before names were kept; it is not filled from the current menu.
ALTER TABLE order_items ADD COLUMN name TEXT;

-- OrderDetails projection (ADR orders 4): the order card of Get Order and List
-- Customer Orders, one row per Order. The repository writes it in the
-- transaction of every change of the Order; queries read only this table.
-- order_version is the Order version the row reflects, projection_version the
-- shape of details. Rows with an older projection_version and Orders without a
-- row are projected again at process start (catch-up); orders-projections
-- rebuild projects every Order again.
CREATE TABLE order_details (
    order_id           TEXT        PRIMARY KEY REFERENCES orders (id) ON DELETE CASCADE,
    customer_id        TEXT        NOT NULL,
    placed_at          TIMESTAMPTZ NOT NULL,
    order_version      BIGINT      NOT NULL CHECK (order_version > 0),
    projection_version INTEGER     NOT NULL CHECK (projection_version > 0),
    details            JSONB       NOT NULL,
    projected_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX order_details_customer_history ON order_details (customer_id, placed_at DESC, order_id DESC);

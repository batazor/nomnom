-- Business storage of orders (ADR 8, ADR 20). References to other bounded
-- contexts are identifiers without foreign keys.
CREATE TABLE orders (
    id                     TEXT PRIMARY KEY,
    customer_id            TEXT        NOT NULL,
    restaurant_id          TEXT        NOT NULL,
    total_amount           NUMERIC     NOT NULL,
    total_currency         TEXT        NOT NULL,
    state                  TEXT        NOT NULL,
    placed_at              TIMESTAMPTZ NOT NULL,
    cancelled_at           TIMESTAMPTZ,
    negative_outcome       TEXT,
    cancellation_code      TEXT,
    version                BIGINT      NOT NULL CHECK (version > 0),
    -- ZedToken of the creation-time order#customer relationship (ADR 18).
    authorization_revision TEXT
);

CREATE INDEX orders_customer_history ON orders (customer_id, placed_at DESC, id DESC);

CREATE TABLE order_items (
    order_id            TEXT    NOT NULL REFERENCES orders (id) ON DELETE CASCADE,
    position            INTEGER NOT NULL,
    menu_item_id        TEXT    NOT NULL,
    quantity            INTEGER NOT NULL CHECK (quantity > 0),
    unit_price_amount   NUMERIC NOT NULL,
    unit_price_currency TEXT    NOT NULL,
    PRIMARY KEY (order_id, menu_item_id)
);

CREATE TABLE order_facts (
    order_id    TEXT        NOT NULL REFERENCES orders (id) ON DELETE CASCADE,
    fact        TEXT        NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL,
    reason      TEXT        NOT NULL DEFAULT '',
    PRIMARY KEY (order_id, fact)
);

-- Transactional Inbox (ADR 11).
CREATE TABLE inbox (
    consumer    TEXT        NOT NULL,
    event_id    TEXT        NOT NULL,
    received_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (consumer, event_id)
);

CREATE TABLE idempotency_keys (
    customer_id TEXT NOT NULL,
    key         TEXT NOT NULL,
    order_id    TEXT NOT NULL REFERENCES orders (id) ON DELETE CASCADE,
    PRIMARY KEY (customer_id, key)
);


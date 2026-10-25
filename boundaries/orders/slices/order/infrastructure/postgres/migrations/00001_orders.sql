-- +goose Up
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

-- Transactional Outbox (ADR 13): watermill-sql PostgreSQL schema and offsets
-- adapters for the forwarder topic orders_outbox.
CREATE TABLE "watermill_orders_outbox" (
    "offset"         BIGSERIAL,
    "uuid"           VARCHAR(36) NOT NULL,
    "created_at"     TIMESTAMP   NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "payload"        JSON DEFAULT NULL,
    "metadata"       JSON DEFAULT NULL,
    "transaction_id" xid8        NOT NULL,
    PRIMARY KEY ("transaction_id", "offset")
);

CREATE TABLE "watermill_offsets_orders_outbox" (
    consumer_group                VARCHAR(255) NOT NULL,
    offset_acked                  BIGINT,
    last_processed_transaction_id xid8         NOT NULL,
    PRIMARY KEY (consumer_group)
);

-- +goose Down
DROP TABLE "watermill_offsets_orders_outbox";
DROP TABLE "watermill_orders_outbox";
DROP TABLE idempotency_keys;
DROP TABLE inbox;
DROP TABLE order_facts;
DROP TABLE order_items;
DROP TABLE orders;

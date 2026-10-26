-- Deliveries and Delivery Offers of delivery (ADR 8, ADR 0001 of the context).
-- References to other bounded contexts (order_id) and to couriers of the
-- courier slice (courier_id) are identifiers without foreign keys.
-- Identifiers use the "C" collation: reconciliation pages by byte order.

CREATE TABLE deliveries (
    id                     TEXT COLLATE "C" PRIMARY KEY,
    order_id               TEXT   NOT NULL UNIQUE,
    courier_id             TEXT COLLATE "C",
    pickup_address         TEXT   NOT NULL,
    state                  TEXT   NOT NULL CHECK (state IN
        ('waiting', 'assigned', 'picked_up', 'delivered', 'failed', 'cancelled')),
    failure_reason         TEXT,
    -- ZedToken of the last delivery#courier relationship write (FR-447).
    authorization_revision TEXT,
    version                BIGINT NOT NULL CHECK (version >= 0)
);

CREATE INDEX deliveries_courier ON deliveries (courier_id) WHERE courier_id IS NOT NULL;

CREATE TABLE delivery_offers (
    id          TEXT COLLATE "C" PRIMARY KEY,
    -- Creation order of offers of one delivery.
    position    BIGSERIAL NOT NULL,
    delivery_id TEXT COLLATE "C" NOT NULL REFERENCES deliveries (id),
    courier_id  TEXT COLLATE "C" NOT NULL,
    offered_at  BIGINT NOT NULL,
    expires_at  BIGINT NOT NULL CHECK (expires_at > offered_at),
    state       TEXT   NOT NULL CHECK (state IN ('pending', 'accepted', 'rejected', 'withdrawn')),
    version     BIGINT NOT NULL CHECK (version >= 0)
);

CREATE INDEX delivery_offers_delivery ON delivery_offers (delivery_id, position);
CREATE INDEX delivery_offers_pending ON delivery_offers (courier_id) WHERE state = 'pending';

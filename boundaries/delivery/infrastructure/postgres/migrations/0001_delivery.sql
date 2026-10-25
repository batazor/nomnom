-- Business storage of delivery (ADR 8, ADR 0001 of the context). References to
-- other bounded contexts (order_id) are identifiers without foreign keys.
-- Identifiers use the "C" collation: reconciliation pages by byte order.

CREATE TABLE couriers (
    id                     TEXT COLLATE "C" PRIMARY KEY,
    status                 TEXT   NOT NULL CHECK (status IN ('available', 'busy', 'unavailable')),
    -- ZedToken of the last courier#self relationship write (ADR 18, FR-447).
    authorization_revision TEXT,
    version                BIGINT NOT NULL CHECK (version >= 0)
);

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

-- Last known courier position: a single upsert, outside the Unit of Work.
CREATE TABLE courier_positions (
    courier_id  TEXT COLLATE "C" PRIMARY KEY,
    latitude    DOUBLE PRECISION NOT NULL,
    longitude   DOUBLE PRECISION NOT NULL,
    recorded_at BIGINT           NOT NULL
);

-- Transactional Outbox (ADR 13, ADR 31): written in the transaction of the
-- aggregate. The polling relay publishes unpublished rows in seq order and sets
-- published_at after the broker acknowledges.
CREATE TABLE delivery_outbox (
    seq               BIGSERIAL PRIMARY KEY,
    -- UUIDv5 of "{aggregate_type}:{aggregate_id}:{aggregate_version}:{event_type}".
    event_id          UUID        NOT NULL UNIQUE,
    -- Full Protobuf message name of the Integration Event.
    event_type        TEXT        NOT NULL,
    aggregate_type    TEXT        NOT NULL,
    aggregate_id      TEXT        NOT NULL,
    aggregate_version BIGINT      NOT NULL,
    occurred_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    topic             TEXT        NOT NULL,
    -- Protobuf payload of nomnom/delivery/v1/events.proto.
    payload           BYTEA       NOT NULL,
    -- traceparent, causation_id.
    metadata          JSONB       NOT NULL DEFAULT '{}',
    published_at      TIMESTAMPTZ,
    attempts          INTEGER     NOT NULL DEFAULT 0,
    last_error        TEXT
);

CREATE INDEX delivery_outbox_unpublished ON delivery_outbox (seq) WHERE published_at IS NULL;

-- Delivery Duration Statistics (FR-676): a read model of the consumer of the
-- Delivery's own facts, not part of the Delivery aggregate. Per Market and leg
-- the latest durations, oldest first by position; the consumer trims each
-- Market and leg to DELIVERY_ESTIMATE__SAMPLE_SIZE after every insert. A
-- Delivery has at most one duration of each leg.
CREATE TABLE delivery_duration_samples (
    position    BIGSERIAL NOT NULL,
    market_id   TEXT COLLATE "C" NOT NULL,
    -- courier_lead: Create Delivery → Pickup; transit: Pickup → handover.
    kind        TEXT   NOT NULL CHECK (kind IN ('courier_lead', 'transit')),
    delivery_id TEXT COLLATE "C" NOT NULL,
    duration_ms BIGINT NOT NULL CHECK (duration_ms >= 0),
    PRIMARY KEY (kind, delivery_id)
);

CREATE INDEX delivery_duration_samples_latest
    ON delivery_duration_samples (market_id, kind, position DESC);

-- Transit of a picked up Delivery that is not handed over yet: opened by the
-- Pickup, closed by the handover (with a transit duration) or by a
-- cancellation or failure (without one).
CREATE TABLE delivery_open_transits (
    delivery_id     TEXT COLLATE "C" PRIMARY KEY,
    market_id       TEXT COLLATE "C" NOT NULL,
    -- Pickup, epoch milliseconds.
    picked_up_at_ms BIGINT NOT NULL
);

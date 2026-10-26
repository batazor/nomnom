-- Market of a courier (ADR 41): set by Register Courier, immutable. Couriers
-- registered before Markets existed belong to the launch Market 'berlin'; the
-- column then becomes required and has no default.

ALTER TABLE couriers ADD COLUMN market_id TEXT COLLATE "C";

UPDATE couriers SET market_id = 'berlin';

ALTER TABLE couriers
    ALTER COLUMN market_id SET NOT NULL,
    ADD CONSTRAINT couriers_market_id_slug CHECK (market_id ~ '^[a-z][a-z0-9-]{1,31}$');

-- Courier Pool of a Market (couriers not retired, by name) and the candidate
-- search of the assignment process (available couriers of the Delivery's
-- Market).
CREATE INDEX couriers_market_pool ON couriers (market_id, name, id) WHERE retired_at IS NULL;
CREATE INDEX couriers_market_status ON couriers (market_id, status);

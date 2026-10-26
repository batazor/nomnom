-- Market of a Delivery (ADR 41): the Market of the Order, set by Create
-- Delivery, immutable. Deliveries created before Markets existed belong to
-- the launch Market 'berlin'; the column then becomes required and has no
-- default.

ALTER TABLE deliveries ADD COLUMN market_id TEXT COLLATE "C";

UPDATE deliveries SET market_id = 'berlin';

ALTER TABLE deliveries
    ALTER COLUMN market_id SET NOT NULL,
    ADD CONSTRAINT deliveries_market_id_slug CHECK (market_id ~ '^[a-z][a-z0-9-]{1,31}$');

-- Backlog of the Courier Pool of a Market: waiting deliveries by Market.
CREATE INDEX deliveries_market_waiting ON deliveries (market_id) WHERE state = 'waiting';

-- Market of the Order (ADR 41): required and immutable. Payments created before Markets
-- belong to the launch Market 'berlin'; the backfill is explicit and the column has no
-- default, so every new row states its Market.
ALTER TABLE payments ADD COLUMN market_id TEXT;

UPDATE payments SET market_id = 'berlin' WHERE market_id IS NULL;

ALTER TABLE payments ALTER COLUMN market_id SET NOT NULL;

ALTER TABLE payments ADD CONSTRAINT payments_market_id_format
    CHECK (market_id ~ '^[a-z][a-z0-9-]{1,31}$');

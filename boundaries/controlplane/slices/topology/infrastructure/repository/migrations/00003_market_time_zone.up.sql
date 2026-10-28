-- Time zone of a Market (FR-689): a required IANA name, immutable like the
-- other values. The existing Markets get it explicitly as a fact, never as a
-- default: the launch Market berlin is in Europe/Berlin. Any other Market has
-- no known time zone, so the migration fails and names it; the operator
-- decides for it before the control plane starts.
ALTER TABLE markets ADD COLUMN time_zone text;

UPDATE markets SET time_zone = 'Europe/Berlin' WHERE market_id = 'berlin';

DO $$
DECLARE
    missing text;
BEGIN
    SELECT string_agg(market_id, ', ' ORDER BY market_id) INTO missing FROM markets WHERE time_zone IS NULL;
    IF missing IS NOT NULL THEN
        RAISE EXCEPTION 'markets without a known time zone: %', missing;
    END IF;
END
$$;

ALTER TABLE markets
    ALTER COLUMN time_zone SET NOT NULL,
    ADD CONSTRAINT markets_time_zone_not_empty CHECK (time_zone <> '');

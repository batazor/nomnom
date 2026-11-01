-- aggregate_version of the Restaurant Order event that named expected_ready_at:
-- a redriven older event does not replace the time. 0 — named before versions
-- were stored.
ALTER TABLE orders
    ADD COLUMN expected_ready_version BIGINT NOT NULL DEFAULT 0;

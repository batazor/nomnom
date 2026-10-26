-- Expected times named by the courier (FR-610), epoch seconds: Pickup time of
-- the current Courier Assignment and handover time named on Pickup. NULL —
-- not named.
ALTER TABLE deliveries
    ADD COLUMN expected_pickup_at BIGINT,
    ADD COLUMN expected_delivered_at BIGINT;

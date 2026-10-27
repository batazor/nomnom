-- Tracking of a Delivery (FR-659): pickup and dropoff intersections of the
-- Virtual City grid and the Tracking Channel of its live courier positions,
-- set by Create Delivery and immutable. Deliveries created before them keep
-- all five columns empty.

ALTER TABLE deliveries
    ADD COLUMN pickup_x INTEGER,
    ADD COLUMN pickup_y INTEGER,
    ADD COLUMN dropoff_x INTEGER,
    ADD COLUMN dropoff_y INTEGER,
    ADD COLUMN tracking_channel TEXT COLLATE "C",
    ADD CONSTRAINT deliveries_tracking_complete CHECK (
        (pickup_x IS NULL AND pickup_y IS NULL AND dropoff_x IS NULL AND dropoff_y IS NULL
            AND tracking_channel IS NULL)
        OR (pickup_x >= 0 AND pickup_y >= 0 AND dropoff_x >= 0 AND dropoff_y >= 0
            AND tracking_channel ~ '^[0-9a-f]{32}$')
    );

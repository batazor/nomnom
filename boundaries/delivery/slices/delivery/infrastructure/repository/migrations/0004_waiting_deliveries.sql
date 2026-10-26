-- Batches of the dispatch job: waiting deliveries in creation order. A
-- Delivery stored before created_at existed counts as created at 0.
CREATE INDEX deliveries_waiting ON deliveries ((COALESCE(created_at, 0)), id)
    WHERE state = 'waiting';

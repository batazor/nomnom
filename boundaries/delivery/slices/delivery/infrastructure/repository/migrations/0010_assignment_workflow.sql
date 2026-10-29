-- Assignment Workflow (FR-704…FR-706): offers expire by a workflow timer, so
-- the batches of the expiry job are gone; a courier who becomes a Courier
-- Candidate wakes the oldest waiting delivery of its Market.
DROP INDEX delivery_offers_pending_expiry;

DROP INDEX deliveries_waiting;
CREATE INDEX deliveries_waiting ON deliveries (market_id, (COALESCE(created_at, 0)), id)
    WHERE state = 'waiting';

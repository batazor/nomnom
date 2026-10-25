-- Delivery Offer Expire transition: a pending offer past its deadline is
-- closed as 'expired' by the background job of the delivery process.
ALTER TABLE delivery_offers DROP CONSTRAINT delivery_offers_state_check;
ALTER TABLE delivery_offers ADD CONSTRAINT delivery_offers_state_check
    CHECK (state IN ('pending', 'accepted', 'rejected', 'withdrawn', 'expired'));

-- Batches of the expiry job: pending offers by deadline.
CREATE INDEX delivery_offers_pending_expiry ON delivery_offers (expires_at, position)
    WHERE state = 'pending';

-- Delivery Failure Reason catalog: failure_reason holds a catalog code,
-- failure_detail the optional free text. Rows written before the catalog keep
-- their free text in failure_reason; the adapter reads such a value as 'other'
-- with the old text as detail, so no data is rewritten here.
ALTER TABLE deliveries ADD COLUMN failure_detail TEXT;

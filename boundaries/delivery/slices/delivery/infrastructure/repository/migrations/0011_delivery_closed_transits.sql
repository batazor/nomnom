-- End of a Delivery handled before its Pickup, as after a redrive of the Pickup
-- from the DLQ: the Pickup takes the record and measures the transit by it.
CREATE TABLE delivery_closed_transits (
    delivery_id       TEXT COLLATE "C" PRIMARY KEY,
    -- Handover, epoch milliseconds; NULL — cancelled or failed, no duration.
    handed_over_at_ms BIGINT,
    closed_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);

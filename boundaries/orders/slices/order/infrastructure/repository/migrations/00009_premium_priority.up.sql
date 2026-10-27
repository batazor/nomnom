-- Premium Priority of the Order (FR-650): chosen at Place Order and immutable,
-- the premium fee fixed then and included in total_amount. Orders placed before
-- had no priority to choose and are STANDARD: the column is added, existing rows
-- are set explicitly once, then it becomes required without a default.
ALTER TABLE orders
    ADD COLUMN priority             TEXT,
    ADD COLUMN premium_fee_amount   NUMERIC,
    ADD COLUMN premium_fee_currency TEXT;
UPDATE orders SET priority = 'STANDARD' WHERE priority IS NULL;
ALTER TABLE orders
    ALTER COLUMN priority SET NOT NULL,
    ADD CONSTRAINT orders_priority_known CHECK (priority IN ('STANDARD', 'PREMIUM')),
    -- A premium Order carries a positive fee, a standard one none.
    ADD CONSTRAINT orders_premium_fee_by_priority CHECK (
        (priority = 'STANDARD' AND premium_fee_amount IS NULL AND premium_fee_currency IS NULL)
        OR (priority = 'PREMIUM' AND premium_fee_amount IS NOT NULL AND premium_fee_currency IS NOT NULL
            AND premium_fee_amount > 0 AND premium_fee_currency ~ '^[A-Z]{3}$')
    );

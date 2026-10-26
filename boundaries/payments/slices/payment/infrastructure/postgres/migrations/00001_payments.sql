-- Business storage of the payment slice (ADR 8, ADR 20, local ADR 1). References to other
-- bounded contexts (order_id) are identifiers without foreign keys.
CREATE TABLE payments (
    id                 TEXT PRIMARY KEY,
    -- One Payment per Order: violation maps to DuplicatePaymentError.
    order_id           TEXT    NOT NULL CONSTRAINT payments_order_id_key UNIQUE,
    amount             NUMERIC NOT NULL CHECK (amount > 0),
    currency           TEXT    NOT NULL,
    state              TEXT    NOT NULL,
    provider_reference TEXT,
    version            BIGINT  NOT NULL CHECK (version > 0)
);

CREATE TABLE payment_operations (
    id                 TEXT PRIMARY KEY,
    payment_id         TEXT        NOT NULL REFERENCES payments (id),
    kind               TEXT        NOT NULL,
    -- Violation maps to DuplicateIdempotencyKeyError.
    idempotency_key    TEXT        NOT NULL
        CONSTRAINT payment_operations_idempotency_key_key UNIQUE,
    amount             NUMERIC     NOT NULL,
    currency           TEXT        NOT NULL,
    requested_at       TIMESTAMPTZ NOT NULL,
    state              TEXT        NOT NULL,
    provider_reference TEXT,
    decline_reason     TEXT,
    completed_at       TIMESTAMPTZ,
    version            BIGINT      NOT NULL CHECK (version > 0)
);

-- At most one operation without a final result per Payment: violation maps to
-- OperationAlreadyPendingError.
CREATE UNIQUE INDEX payment_operations_one_pending
    ON payment_operations (payment_id) WHERE state = 'pending';

CREATE INDEX payment_operations_history ON payment_operations (payment_id, requested_at, id);

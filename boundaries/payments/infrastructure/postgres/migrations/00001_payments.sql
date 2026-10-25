-- Business storage of payments (ADR 8, ADR 20, local ADR 1). References to other
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

-- Transactional Outbox (ADR 13, ADR 31): Integration Events written in the same local
-- transaction as the Payment change; payments-relay publishes them in seq order.
CREATE TABLE outbox (
    seq               BIGSERIAL PRIMARY KEY,
    -- UUIDv5 of "{aggregate_type}:{aggregate_id}:{aggregate_version}:{event_type}".
    event_id          UUID        NOT NULL UNIQUE,
    -- Full Protobuf message name of the contract.
    event_type        TEXT        NOT NULL,
    aggregate_type    TEXT        NOT NULL,
    aggregate_id      TEXT        NOT NULL,
    aggregate_version BIGINT      NOT NULL,
    occurred_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    topic             TEXT        NOT NULL,
    -- Serialized Protobuf message of the contract.
    payload           BYTEA       NOT NULL,
    -- traceparent, causation_id.
    metadata          JSONB       NOT NULL DEFAULT '{}',
    published_at      TIMESTAMPTZ,
    attempts          INT         NOT NULL DEFAULT 0,
    last_error        TEXT
);

CREATE INDEX outbox_unpublished ON outbox (seq) WHERE published_at IS NULL;

-- Transactional Inbox (ADR 11): Kafka consumers and provider callback deduplication
-- (consumer "payment-provider", event_id = provider_message_id).
CREATE TABLE inbox (
    consumer    TEXT        NOT NULL,
    event_id    TEXT        NOT NULL,
    received_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (consumer, event_id)
);

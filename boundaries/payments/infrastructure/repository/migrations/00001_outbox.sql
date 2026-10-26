-- Transactional Outbox (ADR 13, ADR 31, ADR 37): Integration Events written in the same
-- local transaction as the business change; Debezium delivers them from the WAL. One row
-- is one Kafka message: topic, key aggregate_id, value payload and the Event Envelope
-- headers (ADR 25).
CREATE TABLE payments_outbox (
    -- UUIDv5 of "{aggregate_type}:{aggregate_id}:{aggregate_version}:{event_type}".
    event_id          UUID        NOT NULL CONSTRAINT payments_outbox_pkey PRIMARY KEY,
    -- Full Protobuf message name of the contract.
    event_type        TEXT        NOT NULL,
    aggregate_type    TEXT        NOT NULL,
    aggregate_id      TEXT        NOT NULL,
    aggregate_version BIGINT      NOT NULL,
    -- The writer sets occurred_at explicitly (contract field of the record).
    occurred_at       TIMESTAMPTZ NOT NULL,
    topic             TEXT        NOT NULL,
    -- Serialized Protobuf message of the contract.
    payload           BYTEA       NOT NULL,
    -- W3C trace context of the write, '' without one: Debezium needs the field.
    traceparent       TEXT        NOT NULL DEFAULT '',
    -- Insertion time for retention: rows are needed only until the WAL is read.
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX payments_outbox_created_at ON payments_outbox (created_at);

-- Only inserts reach the stream: retention deletes are not events.
CREATE PUBLICATION payments_outbox FOR TABLE payments_outbox WITH (publish = 'insert');

-- +goose Up
-- ADR 37: the Outbox is delivered by Debezium from the WAL, not by the
-- watermill-sql Forwarder. One row is one Kafka message: topic, key
-- aggregate_id, value payload and the Event Envelope headers (ADR 25).
DROP TABLE "watermill_offsets_orders_outbox";
DROP TABLE "watermill_orders_outbox";

CREATE TABLE orders_outbox (
    -- Append order inside a transaction; Debezium follows the WAL, not seq.
    seq               BIGINT      GENERATED ALWAYS AS IDENTITY,
    event_id          UUID        PRIMARY KEY,
    event_type        TEXT        NOT NULL,
    aggregate_type    TEXT        NOT NULL,
    aggregate_id      TEXT        NOT NULL,
    aggregate_version BIGINT      NOT NULL,
    occurred_at       TIMESTAMPTZ NOT NULL,
    topic             TEXT        NOT NULL,
    payload           BYTEA       NOT NULL,
    -- W3C trace context of the write, '' without one: Debezium needs the field.
    traceparent       TEXT        NOT NULL DEFAULT '',
    -- Insertion time for retention: rows are needed only until the WAL is read.
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX orders_outbox_created_at ON orders_outbox (created_at);

-- Only inserts reach the stream: retention deletes are not events.
CREATE PUBLICATION orders_outbox FOR TABLE orders_outbox WITH (publish = 'insert');

-- +goose Down
DROP PUBLICATION orders_outbox;
DROP TABLE orders_outbox;

CREATE TABLE "watermill_orders_outbox" (
    "offset"         BIGSERIAL,
    "uuid"           VARCHAR(36) NOT NULL,
    "created_at"     TIMESTAMP   NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "payload"        JSON DEFAULT NULL,
    "metadata"       JSON DEFAULT NULL,
    "transaction_id" xid8        NOT NULL,
    PRIMARY KEY ("transaction_id", "offset")
);

CREATE TABLE "watermill_offsets_orders_outbox" (
    consumer_group                VARCHAR(255) NOT NULL,
    offset_acked                  BIGINT,
    last_processed_transaction_id xid8         NOT NULL,
    PRIMARY KEY (consumer_group)
);

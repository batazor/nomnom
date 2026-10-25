-- ADR 37: the Outbox is delivered by Debezium from the WAL, not by payments-relay.
-- One row is one Kafka message: topic, key aggregate_id, value payload and the Event
-- Envelope headers (ADR 25). Rows not yet published by the relay are not replayed:
-- the connector starts from the WAL position of its slot (snapshot.mode no_data).
ALTER TABLE outbox RENAME TO payments_outbox;

-- W3C trace context of the write, '' without one: Debezium needs the field.
ALTER TABLE payments_outbox ADD COLUMN traceparent TEXT NOT NULL DEFAULT '';
UPDATE payments_outbox SET traceparent = metadata ->> 'traceparent'
    WHERE metadata ->> 'traceparent' IS NOT NULL;

-- Relay-only state: publish order, delivery bookkeeping and the stored trace metadata.
DROP INDEX outbox_unpublished;
ALTER TABLE payments_outbox
    DROP COLUMN seq,
    DROP COLUMN metadata,
    DROP COLUMN published_at,
    DROP COLUMN attempts,
    DROP COLUMN last_error,
    DROP CONSTRAINT outbox_event_id_key,
    ADD CONSTRAINT payments_outbox_pkey PRIMARY KEY (event_id),
    -- The writer sets occurred_at explicitly (contract field of the record).
    ALTER COLUMN occurred_at DROP DEFAULT,
    -- Insertion time for retention: rows are needed only until the WAL is read.
    ADD COLUMN created_at TIMESTAMPTZ NOT NULL DEFAULT now();

CREATE INDEX payments_outbox_created_at ON payments_outbox (created_at);

-- Only inserts reach the stream: retention deletes are not events.
CREATE PUBLICATION payments_outbox FOR TABLE payments_outbox WITH (publish = 'insert');

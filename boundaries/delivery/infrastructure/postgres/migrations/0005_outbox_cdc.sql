-- Outbox delivery via CDC (ADR 37): Debezium reads delivery_outbox inserts from
-- the WAL, so the table holds only the outbox record contract. traceparent
-- moves out of metadata into its own column ('' when there is none, never
-- NULL); relay columns go away.
ALTER TABLE delivery_outbox ADD COLUMN traceparent TEXT NOT NULL DEFAULT '';
UPDATE delivery_outbox SET traceparent = metadata->>'traceparent'
    WHERE metadata->>'traceparent' IS NOT NULL;

-- Retention: every Outbox write deletes rows older than an hour by created_at;
-- the replication slot keeps the WAL independently of the table.
ALTER TABLE delivery_outbox ADD COLUMN created_at TIMESTAMPTZ NOT NULL DEFAULT now();
UPDATE delivery_outbox SET created_at = occurred_at;
CREATE INDEX delivery_outbox_created_at ON delivery_outbox (created_at);

DROP INDEX delivery_outbox_unpublished;
ALTER TABLE delivery_outbox
    DROP COLUMN seq,
    DROP COLUMN metadata,
    DROP COLUMN published_at,
    DROP COLUMN attempts,
    DROP COLUMN last_error,
    DROP CONSTRAINT delivery_outbox_event_id_key,
    ADD PRIMARY KEY (event_id);

-- Inserts only: retention deletes do not reach the stream. The connector
-- creates the slot delivery_outbox and does not create publications.
CREATE PUBLICATION delivery_outbox FOR TABLE delivery_outbox WITH (publish = 'insert');

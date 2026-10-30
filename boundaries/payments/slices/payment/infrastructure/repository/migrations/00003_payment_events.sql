-- Event Sourcing of Payment (local ADR 2): payment_events becomes the source of truth,
-- payments and payment_operations its synchronous projections. Existing rows have no
-- stream and are not migrated: a non-empty table stops the migration.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM payments) THEN
        RAISE EXCEPTION 'payments must be empty: existing rows have no event stream';
    END IF;
END
$$;

CREATE TABLE payment_events (
    payment_id     TEXT        NOT NULL,
    version        BIGINT      NOT NULL CHECK (version > 0),
    event_type     TEXT        NOT NULL,
    schema_version INT         NOT NULL CHECK (schema_version > 0),
    payload        JSONB       NOT NULL,
    -- W3C trace context of the write, '' without one, as in the Outbox.
    traceparent    TEXT        NOT NULL,
    recorded_at    TIMESTAMPTZ NOT NULL,
    -- Optimistic concurrency: a second writer of a version maps to ConcurrencyConflictError.
    CONSTRAINT payment_events_pkey PRIMARY KEY (payment_id, version)
);

CREATE FUNCTION payment_events_reject_change() RETURNS trigger
    LANGUAGE plpgsql AS
$$
BEGIN
    RAISE EXCEPTION 'payment_events is append-only';
END
$$;

-- Row trigger: UPDATE and DELETE are rejected, TRUNCATE stays available to tests.
CREATE TRIGGER payment_events_append_only
    BEFORE UPDATE OR DELETE ON payment_events
    FOR EACH ROW EXECUTE FUNCTION payment_events_reject_change();

-- The latest snapshot of a Payment: derived data, emptying the table loses nothing.
CREATE TABLE payment_snapshots (
    payment_id     TEXT        PRIMARY KEY,
    version        BIGINT      NOT NULL CHECK (version > 0),
    schema_version INT         NOT NULL CHECK (schema_version > 0),
    state          JSONB       NOT NULL,
    recorded_at    TIMESTAMPTZ NOT NULL
);

-- An operation is part of the Payment aggregate and has no version of its own.
ALTER TABLE payment_operations DROP COLUMN version;

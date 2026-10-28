-- Transactional Inbox of delivery (ADR 11), shared by every consumer of the
-- process: an event processed by a consumer is recorded in the transaction of
-- its effect, so a redelivered event_id changes nothing. Retention: every
-- claim deletes rows older than seven days in the same statement, longer than
-- a redelivery of the stream can lag.
CREATE TABLE delivery_inbox (
    consumer     TEXT        NOT NULL,
    -- event_id of the Event Envelope.
    event_id     TEXT        NOT NULL,
    event_type   TEXT        NOT NULL,
    processed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (consumer, event_id)
);

CREATE INDEX delivery_inbox_processed_at ON delivery_inbox (processed_at);

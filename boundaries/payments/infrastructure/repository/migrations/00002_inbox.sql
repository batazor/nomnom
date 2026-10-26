-- Transactional Inbox (ADR 11): Kafka consumers and provider callback deduplication
-- (consumer "payment-provider", event_id = provider_message_id).
CREATE TABLE inbox (
    consumer    TEXT        NOT NULL,
    event_id    TEXT        NOT NULL,
    received_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (consumer, event_id)
);

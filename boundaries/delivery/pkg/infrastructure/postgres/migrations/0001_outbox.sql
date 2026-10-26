-- Transactional Outbox of delivery (ADR 13, ADR 31), shared by every slice:
-- written in the transaction of the aggregate. The polling relay publishes
-- unpublished rows in seq order and sets published_at after the broker
-- acknowledges.
CREATE TABLE delivery_outbox (
    seq               BIGSERIAL PRIMARY KEY,
    -- UUIDv5 of "{aggregate_type}:{aggregate_id}:{aggregate_version}:{event_type}".
    event_id          UUID        NOT NULL UNIQUE,
    -- Full Protobuf message name of the Integration Event.
    event_type        TEXT        NOT NULL,
    aggregate_type    TEXT        NOT NULL,
    aggregate_id      TEXT        NOT NULL,
    aggregate_version BIGINT      NOT NULL,
    occurred_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    topic             TEXT        NOT NULL,
    -- Protobuf payload of nomnom/delivery/v1/events.proto.
    payload           BYTEA       NOT NULL,
    -- traceparent, causation_id.
    metadata          JSONB       NOT NULL DEFAULT '{}',
    published_at      TIMESTAMPTZ,
    attempts          INTEGER     NOT NULL DEFAULT 0,
    last_error        TEXT
);

CREATE INDEX delivery_outbox_unpublished ON delivery_outbox (seq) WHERE published_at IS NULL;

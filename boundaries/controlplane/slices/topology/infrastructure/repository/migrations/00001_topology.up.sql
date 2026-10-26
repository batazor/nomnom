-- Registry of the control plane (ADR 41): Cells and Markets with their Market
-- Placement. Ids and values are immutable; the API never updates a row.
CREATE TABLE cells (
    cell_id         text PRIMARY KEY CHECK (cell_id ~ '^[a-z][a-z0-9-]{1,31}$'),
    gateway_address text NOT NULL CHECK (gateway_address <> ''),
    registered_at   timestamptz NOT NULL
);

-- cell_id is the Market Placement: a Market is served by exactly one registered Cell.
CREATE TABLE markets (
    market_id  text PRIMARY KEY CHECK (market_id ~ '^[a-z][a-z0-9-]{1,31}$'),
    name       text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 64 AND name = btrim(name)),
    currency   text NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
    cell_id    text NOT NULL REFERENCES cells (cell_id),
    created_at timestamptz NOT NULL
);

CREATE INDEX markets_cell_id_idx ON markets (cell_id);

-- Couriers of delivery (ADR 8, ADR 0001 of the context). Identifiers use the
-- "C" collation: reconciliation pages by byte order.

CREATE TABLE couriers (
    id                     TEXT COLLATE "C" PRIMARY KEY,
    status                 TEXT   NOT NULL CHECK (status IN ('available', 'busy', 'unavailable')),
    -- ZedToken of the last courier#self relationship write (ADR 18, FR-447).
    authorization_revision TEXT,
    version                BIGINT NOT NULL CHECK (version >= 0)
);

-- Last known courier position: a single upsert, outside the Unit of Work.
CREATE TABLE courier_positions (
    courier_id  TEXT COLLATE "C" PRIMARY KEY,
    latitude    DOUBLE PRECISION NOT NULL,
    longitude   DOUBLE PRECISION NOT NULL,
    recorded_at BIGINT           NOT NULL
);

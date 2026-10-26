-- Courier Pool (change add-cook-and-courier-pools, FR-301, FR-619): the name of
-- a courier and its retirement. The name has no default: a stored courier
-- without one is not a valid courier, so a store with couriers is recreated
-- (kind stores are emptyDir and seeded again).

ALTER TABLE couriers
    ADD COLUMN name       TEXT   NOT NULL
        CHECK (btrim(name) = name AND char_length(name) BETWEEN 1 AND 64),
    -- Moment the courier left the pool; a retired courier stays unavailable.
    ADD COLUMN retired_at BIGINT,
    ADD CONSTRAINT couriers_retired_unavailable
        CHECK (retired_at IS NULL OR status = 'unavailable');

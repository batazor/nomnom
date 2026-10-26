-- Switch of the demo customer Simulator (FR-604).
-- At most one row; no row means the Simulator is disabled.
CREATE TABLE simulator_state (
    singleton  BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (singleton),
    enabled    BOOLEAN NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

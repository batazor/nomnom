-- Demo Simulator switch of the process (FR-604): at most one row; no row
-- means the Simulator is disabled.
CREATE TABLE simulator (
    singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (singleton),
    enabled BOOLEAN NOT NULL
);

-- Rotation of the KEK (local ADR 2): a wrapped key records the KEK version that opens it.
-- An active key without a version fails the constraint: its derivation predates this schema.
ALTER TABLE subject_keys ADD COLUMN kek_version INT CHECK (kek_version > 0);

ALTER TABLE subject_keys DROP CONSTRAINT subject_keys_forgotten;

ALTER TABLE subject_keys ADD CONSTRAINT subject_keys_forgotten CHECK (
    (forgotten_at IS NULL AND subject_ref IS NOT NULL AND wrapped_key IS NOT NULL
        AND kek_version IS NOT NULL)
    OR (forgotten_at IS NOT NULL AND subject_ref IS NULL AND wrapped_key IS NULL
        AND kek_version IS NULL)
);

-- Keys left behind after a rotation are found without scanning the whole table.
CREATE INDEX subject_keys_kek_version ON subject_keys (kek_version) WHERE kek_version IS NOT NULL;

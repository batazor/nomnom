-- Crypto-shredding of user identifiers in the Payment streams (local ADR 2). A plaintext user
-- identifier already in the immutable stream could never be forgotten, so it stops the migration.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM payment_events
        WHERE event_type = 'PaymentOperationRequested'
          AND payload -> 'requested_by' ->> 'kind' = 'user'
    ) THEN
        RAISE EXCEPTION 'payment_events holds plaintext user identifiers that cannot be forgotten';
    END IF;
END
$$;

-- One key per user. subject_ref is an HMAC, so the table does not reveal the identifier itself.
-- Forgetting clears the key and the reference and keeps the row as a mark.
CREATE TABLE subject_keys (
    key_id       TEXT        PRIMARY KEY,
    subject_ref  BYTEA       UNIQUE,
    wrapped_key  BYTEA,
    created_at   TIMESTAMPTZ NOT NULL,
    forgotten_at TIMESTAMPTZ,
    CONSTRAINT subject_keys_forgotten CHECK (
        (forgotten_at IS NULL AND subject_ref IS NOT NULL AND wrapped_key IS NOT NULL)
        OR (forgotten_at IS NOT NULL AND subject_ref IS NULL AND wrapped_key IS NULL)
    )
);

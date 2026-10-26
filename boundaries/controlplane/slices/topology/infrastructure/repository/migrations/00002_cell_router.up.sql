-- A Cell is known by the Service of its Cosmo Router (ADR 41): the public
-- gateway routes its Markets there. The internal gateway address is no longer
-- part of the registry. Router values of the registered Cells cannot be
-- derived, so the registry is emptied; the operator registers the Cells and
-- Markets again (in kind the seed Job does it, idempotently).
DELETE FROM markets;
DELETE FROM cells;

ALTER TABLE cells
    DROP COLUMN gateway_address,
    ADD COLUMN router_namespace text NOT NULL CHECK (router_namespace ~ '^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$'),
    ADD COLUMN router_name text NOT NULL CHECK (router_name ~ '^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$'),
    ADD COLUMN router_port integer NOT NULL CHECK (router_port BETWEEN 1 AND 65535);

-- Archive of a Market (ADR 41): archived_at is set once by ArchiveMarket and
-- never cleared. An archived Market leaves the list of Markets and the public
-- gateway; its row stays, so its id is never created again. The other values
-- of a Market stay immutable.
ALTER TABLE markets ADD COLUMN archived_at timestamptz;

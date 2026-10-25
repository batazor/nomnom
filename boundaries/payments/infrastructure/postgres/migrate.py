"""Forward-only SQL migrations of the payments database, applied by yoyo-migrations.

Files ``migrations/<version>_<name>.sql`` are applied in file name order, each in its
own transaction together with its ``_yoyo_migration`` row. The ``payments`` process
applies them at startup; a session advisory lock serializes concurrent runs (several
replicas starting together), so the rest find nothing to change. The lock is held by
PostgreSQL for the session, so a process killed mid-migration releases it with its
connection; the yoyo lock row (``yoyo_lock``) would stay behind and is not used.
"""

import psycopg
from psycopg.conninfo import conninfo_to_dict
from yoyo import read_migrations
from yoyo.backends import get_backend_class
from yoyo.connections import DatabaseURI
from yoyo.migrations import MigrationList, default_migration_table

from pkg.config import DatabaseSettings

from .database import connect_timeout_seconds

# Бэкенд yoyo на psycopg 3.
_BACKEND = "postgresql+psycopg"
_SOURCE = f"package:{__package__}:migrations"
# Arbitrary constant shared by every payments migrator.
_LOCK_KEY = 0x7061796D656E7473  # "payments"
# Bound of every lock wait of a migration. Migrations run during a rolling update next
# to live traffic: an ALTER TABLE queued behind a long transaction would stall every
# query of the table behind it, so it fails fast and the pod retries on restart.
_LOCK_TIMEOUT = "-c lock_timeout=5s"


def migrations() -> MigrationList:
    """Return the bundled migrations in the order they are applied."""
    return read_migrations(_SOURCE)


def migrate(settings: DatabaseSettings) -> list[str]:
    """Apply pending migrations on one connection and return the ids of the applied ones."""
    # Параметры libpq DSN (URI или key=value) передаются в psycopg.connect как есть.
    params = conninfo_to_dict(
        settings.url, connect_timeout=connect_timeout_seconds(settings.connect_timeout)
    )
    # The lock is taken before yoyo creates its tables: concurrent CREATE TABLE IF NOT
    # EXISTS of several replicas collide in the catalog.
    with psycopg.connect(**params, autocommit=True) as lock:
        lock.execute("SELECT pg_advisory_lock(%s)", (_LOCK_KEY,))
        try:
            # Only the yoyo connection: the advisory lock waits for another replica unbounded.
            options = " ".join(o for o in (params.get("options"), _LOCK_TIMEOUT) if o)
            uri = DatabaseURI(
                _BACKEND, None, None, None, None, None, {**params, "options": options}
            )
            backend = get_backend_class(_BACKEND)(uri, default_migration_table)
            try:
                backend.init_database()
                pending = backend.to_apply(migrations())
                backend.apply_migrations(pending)
                return [migration.id for migration in pending]
            finally:
                backend.connection.close()
        finally:
            lock.execute("SELECT pg_advisory_unlock(%s)", (_LOCK_KEY,))

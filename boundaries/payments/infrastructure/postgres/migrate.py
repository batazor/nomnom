"""Forward-only SQL migrations of the payments database, applied by yoyo-migrations.

Files ``migrations/<version>_<name>.sql`` are applied in file name order, each in its
own transaction together with its ``_yoyo_migration`` row. The ``payments`` process
applies them at startup; the yoyo lock (a row in ``yoyo_lock``) serializes concurrent
runs (several replicas starting together), so the rest find nothing to change.
"""

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
# Ожидание lock-а другой реплики; строка lock-а процесса, убитого посреди миграции,
# остаётся в yoyo_lock и снимается вручную (`yoyo break-lock`).
_LOCK_TIMEOUT_SECONDS = 60


def migrations() -> MigrationList:
    """Return the bundled migrations in the order they are applied."""
    return read_migrations(_SOURCE)


def migrate(settings: DatabaseSettings) -> list[str]:
    """Apply pending migrations on one connection and return the ids of the applied ones."""
    # Параметры libpq DSN (URI или key=value) передаются в psycopg.connect как есть.
    params = conninfo_to_dict(
        settings.url, connect_timeout=connect_timeout_seconds(settings.connect_timeout)
    )
    uri = DatabaseURI(_BACKEND, None, None, None, None, None, params)
    backend = get_backend_class(_BACKEND)(uri, default_migration_table)
    try:
        backend.init_database()
        with backend.lock(timeout=_LOCK_TIMEOUT_SECONDS):
            pending = backend.to_apply(migrations())
            backend.apply_migrations(pending)
        return [migration.id for migration in pending]
    finally:
        backend.connection.close()

"""Forward-only SQL migrations of the payments database, applied by yoyo-migrations.

The schema is split into migration sets by owner: ``infrastructure`` holds the tables shared
by every slice (Outbox, Inbox), each slice holds its own tables. A set is the
``migrations/<version>_<name>.sql`` directory of its package with its own yoyo tables
``_yoyo_migration_<owner>``, ``_yoyo_log_<owner>`` and ``_yoyo_version_<owner>``, so every
set numbers its files from 1 independently.
Sets are applied in the order the caller lists them; files of a set in file name order,
each in its own transaction together with its migration row.

The ``payments`` process applies them at startup; a session advisory lock serializes
concurrent runs (several replicas starting together), so the rest find nothing to change.
The lock is held by PostgreSQL for the session, so a process killed mid-migration releases
it with its connection; the yoyo lock row (``yoyo_lock``) would stay behind and is not used.
"""

from collections.abc import Sequence
from dataclasses import dataclass

import psycopg
from psycopg.conninfo import conninfo_to_dict
from yoyo import read_migrations
from yoyo.backends import get_backend_class
from yoyo.connections import DatabaseURI
from yoyo.migrations import MigrationList

from pkg.config import DatabaseSettings

from .database import connect_timeout_seconds

# Бэкенд yoyo на psycopg 3.
_BACKEND = "postgresql+psycopg"
# Arbitrary constant shared by every payments migrator.
_LOCK_KEY = 0x7061796D656E7473  # "payments"
# Bound of every lock wait of a migration. Migrations run during a rolling update next
# to live traffic: an ALTER TABLE queued behind a long transaction would stall every
# query of the table behind it, so it fails fast and the pod retries on restart.
_LOCK_TIMEOUT = "-c lock_timeout=5s"


class MissingMigrationsError(RuntimeError):
    """A migration set has no ``migrations/*.sql`` in its package."""


@dataclass(frozen=True, slots=True)
class MigrationSet:
    """Migrations of one owner: ``migrations/*.sql`` of ``package``, tracked separately."""

    owner: str
    package: str

    @property
    def migration_table(self) -> str:
        """Yoyo table of the applied migrations of the set."""
        return f"_yoyo_migration_{self.owner}"

    @property
    def log_table(self) -> str:
        """Yoyo journal of the set."""
        return f"_yoyo_log_{self.owner}"

    @property
    def version_table(self) -> str:
        """Yoyo internal schema version of the set."""
        return f"_yoyo_version_{self.owner}"

    def migrations(self) -> MigrationList:
        """Return the bundled migrations of the set in the order they are applied."""
        # yoyo пропускает отсутствующий каталог молча: пустой набор — ошибка сборки.
        found = read_migrations(f"package:{self.package}:migrations")
        if not found:
            raise MissingMigrationsError(f"no migrations in {self.package}/migrations")
        return found


def migrate(settings: DatabaseSettings, sets: Sequence[MigrationSet]) -> list[str]:
    """Apply pending migrations of ``sets`` in order under one lock.

    Returns ``<owner>/<id>`` of the applied migrations.
    """
    # Параметры libpq DSN (URI или key=value) передаются в psycopg.connect как есть.
    params = conninfo_to_dict(
        settings.url, connect_timeout=connect_timeout_seconds(settings.connect_timeout)
    )
    # The lock is taken before yoyo creates its tables: concurrent CREATE TABLE IF NOT
    # EXISTS of several replicas collide in the catalog.
    with psycopg.connect(**params, autocommit=True) as lock:
        lock.execute("SELECT pg_advisory_lock(%s)", (_LOCK_KEY,))
        try:
            # Only the yoyo connections: the advisory lock waits for another replica unbounded.
            options = " ".join(o for o in (params.get("options"), _LOCK_TIMEOUT) if o)
            uri = DatabaseURI(
                _BACKEND, None, None, None, None, None, {**params, "options": options}
            )
            applied: list[str] = []
            for migration_set in sets:
                applied.extend(_apply(uri, migration_set))
            return applied
        finally:
            lock.execute("SELECT pg_advisory_unlock(%s)", (_LOCK_KEY,))


def _apply(uri: DatabaseURI, migration_set: MigrationSet) -> list[str]:
    """Apply pending migrations of one set on its own yoyo connection."""
    backend = get_backend_class(_BACKEND)(uri, migration_set.migration_table)
    # Журнал и версия внутренней схемы yoyo общие для всех migration tables схемы: для
    # второй таблицы yoyo запустил бы upgrade, который создаёт их заново и копирует в неё
    # чужой журнал. Поэтому у каждого набора свои.
    backend.log_table = migration_set.log_table
    backend.version_table = migration_set.version_table
    try:
        backend.init_database()
        pending = backend.to_apply(migration_set.migrations())
        backend.apply_migrations(pending)
        return [f"{migration_set.owner}/{migration.id}" for migration in pending]
    finally:
        backend.connection.close()

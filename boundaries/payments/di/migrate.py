"""Composition root of ``payments-migrate``: applies PostgreSQL migrations and exits.

Opens no pool: ``migrate`` holds a single connection under an advisory lock.
"""

from infrastructure.postgres import migrate
from pkg.config import DatabaseSettings


def run() -> list[str]:
    """Return the names of the migration files applied by this run."""
    return migrate(DatabaseSettings().url)

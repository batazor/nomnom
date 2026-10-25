"""Container of ``payments-migrate``: applies PostgreSQL migrations and exits (ADR 5).

Opens no pool: ``migrate`` holds a single connection under an advisory lock.
"""

from dependency_injector import containers, providers

from infrastructure.postgres import migrate


class MigrateContainer(containers.DeclarativeContainer):
    """One instance per ``payments-migrate`` run; ``config.database`` is ``DatabaseSettings``."""

    config = providers.Configuration(strict=True)

    # Returns the names of the migration files applied by this run.
    migrations = providers.Callable(migrate, config.database.provided.url)

"""Process resources shared by the payments composition roots.

Each resource is a context manager: entering initializes it, leaving releases it, so a
composition root owns the resource by the ``with`` block around its workers.
"""

from collections.abc import Generator
from contextlib import contextmanager
from datetime import timedelta

from infrastructure.postgres import PostgresDatabase, create_database
from pkg import telemetry
from pkg.config import DatabaseSettings, ManagementSettings, ShutdownSettings
from pkg.lifecycle import Lifecycle, LifecycleConfig


@contextmanager
def telemetry_providers(flush_timeout: timedelta) -> Generator[telemetry.Telemetry, None, None]:
    """Global tracer and meter providers; flushed within ``flush_timeout`` on exit.

    Telemetry shutdown is the last step of the runtime contract
    (docs/cloud-native-runtime.md, step 8), so it is the outermost resource.
    """
    providers = telemetry.setup()
    try:
        yield providers
    finally:
        providers.shutdown(flush_timeout.total_seconds())


@contextmanager
def postgres_database(settings: DatabaseSettings) -> Generator[PostgresDatabase, None, None]:
    """psycopg pool; an unreachable database stops the process before ports open.

    The schema is applied by ``payments-migrate`` (init container), not here.
    """
    database = create_database(settings)
    try:
        database.check()
        yield database
    finally:
        database.close()


def lifecycle(management: ManagementSettings, shutdown: ShutdownSettings) -> Lifecycle:
    return Lifecycle(
        LifecycleConfig(
            management_port=management.port,
            readiness_delay=shutdown.readiness_delay.total_seconds(),
            drain_timeout=shutdown.drain_timeout.total_seconds(),
        ),
    )

"""Resources and configuration shared by the payments containers (ADR 5).

Resources are generator initializers of ``providers.Resource``: code before ``yield``
initializes, code after it releases. Two Resource provider types fix the order the
runtime contract requires (docs/cloud-native-runtime.md): ``TelemetryResource`` is
initialized first and shut down last, ``ProcessResource`` (database, clients) lives
inside it. An entrypoint therefore calls::

    container.init_resources(TelemetryResource)
    try:
        container.init_resources(ProcessResource)
        ...
    finally:
        try:
            container.shutdown_resources(ProcessResource)
        finally:
            container.shutdown_resources(TelemetryResource)

Within a type, ``shutdown_resources`` releases dependents before their dependencies.
"""

import logging
from collections.abc import Iterator
from dataclasses import fields
from datetime import timedelta
from typing import Any

from dependency_injector import providers

from infrastructure.postgres import PostgresDatabase, create_database, migrate
from pkg import telemetry
from pkg.config import (
    DatabaseSettings,
    ManagementSettings,
    Settings,
    ShutdownSettings,
)
from pkg.lifecycle import Lifecycle, LifecycleConfig

logger = logging.getLogger(__name__)


class TelemetryResource[T](providers.Resource[T]):
    """Telemetry providers: initialized before and flushed after every other resource."""


class ProcessResource[T](providers.Resource[T]):
    """Process-wide client or pool, owned for the lifetime of the workers."""


# ``Configuration.provider`` is the provider's own attribute, so the section is renamed.
_SECTION_KEYS = {"provider": "payment_provider"}


def sections(settings: Settings) -> dict[str, Any]:
    """Validated pydantic settings by section for ``providers.Configuration.from_dict``.

    Sections stay typed settings objects: adapters receive ``config.<section>`` as is.
    """
    return {
        _SECTION_KEYS.get(field.name, field.name): getattr(settings, field.name)
        for field in fields(settings)
    }


def telemetry_providers(flush_timeout: timedelta) -> Iterator[telemetry.Telemetry]:
    """Global tracer and meter providers; flushed within ``flush_timeout`` on shutdown.

    The default ``service.name`` is ``payments``; ``OTEL_SERVICE_NAME`` overrides it.
    """
    owner = telemetry.setup("payments")
    try:
        yield owner
    finally:
        owner.shutdown(flush_timeout.total_seconds())


def postgres_database(settings: DatabaseSettings) -> Iterator[PostgresDatabase]:
    """The psycopg pool over the migrated schema; any failure stops the process before ports open.

    Pending migrations are applied first, on one connection under the advisory lock of
    ``migrate``: replicas starting together apply them once, the rest find nothing to
    change. The pool opens only after that connection is closed.
    """
    applied = migrate(settings)
    logger.info("payments migrations applied: %s", ", ".join(applied) or "none pending")
    database = create_database(settings)
    try:
        database.check()
        yield database
    finally:
        database.close()


def listen_all(port: int) -> tuple[str, int]:
    """Bind address on every interface of the container for ``port``."""
    return ("0.0.0.0", port)  # noqa: S104 - containers listen on all interfaces


def create_lifecycle(management: ManagementSettings, shutdown: ShutdownSettings) -> Lifecycle:
    """Process lifecycle with the configured management port and shutdown timings."""
    return Lifecycle(
        LifecycleConfig(
            management_port=management.port,
            readiness_delay=shutdown.readiness_delay.total_seconds(),
            drain_timeout=shutdown.drain_timeout.total_seconds(),
        ),
    )

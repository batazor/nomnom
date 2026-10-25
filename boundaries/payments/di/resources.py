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

from collections.abc import Iterator
from dataclasses import fields
from datetime import timedelta
from typing import Any

from dependency_injector import providers

from infrastructure.postgres import PostgresDatabase, create_database
from pkg import telemetry
from pkg.config import (
    DatabaseSettings,
    ManagementSettings,
    RelaySettings,
    Settings,
    ShutdownSettings,
)
from pkg.lifecycle import Lifecycle, LifecycleConfig


class TelemetryResource(providers.Resource):
    """Telemetry providers: initialized before and flushed after every other resource."""


class ProcessResource(providers.Resource):
    """Process-wide client or pool, owned for the lifetime of the workers."""


# ``Configuration.provider`` is the provider's own attribute, so the section is renamed.
_SECTION_KEYS = {"provider": "payment_provider"}


def sections(settings: Settings | RelaySettings) -> dict[str, Any]:
    """Validated pydantic settings by section for ``providers.Configuration.from_dict``.

    Sections stay typed settings objects: adapters receive ``config.<section>`` as is.
    """
    return {
        _SECTION_KEYS.get(field.name, field.name): getattr(settings, field.name)
        for field in fields(settings)
    }


def telemetry_providers(flush_timeout: timedelta) -> Iterator[telemetry.Telemetry]:
    """Global tracer and meter providers; flushed within ``flush_timeout`` on shutdown."""
    owner = telemetry.setup()
    try:
        yield owner
    finally:
        owner.shutdown(flush_timeout.total_seconds())


def postgres_database(settings: DatabaseSettings) -> Iterator[PostgresDatabase]:
    """The psycopg pool; an unreachable database stops the process before ports open.

    The schema is applied by ``payments-migrate`` (init container), not here.
    """
    database = create_database(settings)
    try:
        database.check()
        yield database
    finally:
        database.close()


def listen_all(port: int) -> tuple[str, int]:
    """Bind address on every interface of the container for ``port``."""
    return ("0.0.0.0", port)  # noqa: S104 - containers listen on all interfaces


def seconds(value: timedelta) -> float:
    """Convert a configured duration to the float seconds that adapters expect."""
    return value.total_seconds()


def create_lifecycle(management: ManagementSettings, shutdown: ShutdownSettings) -> Lifecycle:
    """Process lifecycle with the configured management port and shutdown timings."""
    return Lifecycle(
        LifecycleConfig(
            management_port=management.port,
            readiness_delay=shutdown.readiness_delay.total_seconds(),
            drain_timeout=shutdown.drain_timeout.total_seconds(),
        ),
    )

"""Configuration of payments: the file at ``CONFIG_FILE`` plus secrets from env (ADR 17).

A validation error stops the process before any port is open.
"""

from dataclasses import dataclass

from pydantic import ValidationError

from . import database
from .database import (
    DATABASE_URI_ENV,
    DEFAULT_DATABASE_MAX_CONNS,
    DatabaseSpec,
)
from .duration import parse_duration
from .event_store import (
    EVENT_STORE_SUBJECT_KEKS_ENV,
    EVENT_STORE_SUBJECT_REF_KEY_ENV,
    EventStoreSettings,
    EventStoreSpec,
)
from .file import (
    API_VERSION,
    CONFIG_FILE_ENV,
    KIND,
    ConfigError,
    Spec,
    describe,
    load_spec,
    parse_document,
    parse_spec,
)
from .identity import (
    IDENTITY_AUDIENCE_KEY,
    IDENTITY_ISSUER_KEY,
    IDENTITY_JWKS_FILE_KEY,
    IDENTITY_SERVICE_ACCOUNTS_KEY,
    IDENTITY_USER_DELEGATES_KEY,
    IDENTITY_USER_ISSUER_KEY,
    IDENTITY_USER_JWKS_FILE_KEY,
    IdentitySettings,
    parse_service_accounts,
    parse_user_delegates,
)
from .provider import (
    DEFAULT_PROVIDER_WEBHOOK_PORT,
    DEFAULT_PROVIDER_WEBHOOK_TOLERANCE,
    PROVIDER_KINDS,
    PROVIDER_STRIPE_API_KEY_ENV,
    PROVIDER_WEBHOOK_SECRET_ENV,
    ProviderSettings,
    ProviderSpec,
)
from .runtime import (
    DEFAULT_DRAIN_TIMEOUT,
    DEFAULT_MANAGEMENT_PORT,
    DEFAULT_PORT,
    DEFAULT_READINESS_DELAY,
    DEFAULT_TELEMETRY_FLUSH_TIMEOUT,
    ManagementSettings,
    ServerSettings,
    ShutdownSettings,
)
from .spicedb import (
    DEFAULT_SPICEDB_TIMEOUT,
    SPICEDB_PRESHARED_KEY_ENV,
    SpiceDBSettings,
    SpiceDBSpec,
)
from .telemetry import (
    DEFAULT_METRICS_EXPORT_INTERVAL,
    LOG_LEVELS,
    POD_NAME_ENV,
    TELEMETRY_PROTOCOLS,
    LoggingSpec,
    LogLevel,
    MetricsSpec,
    TelemetrySettings,
    TelemetrySpec,
    TracingSpec,
)

__all__ = [
    "ADMIN_PROCESS_NAME",
    "API_VERSION",
    "CONFIG_FILE_ENV",
    "DATABASE_URI_ENV",
    "DEFAULT_DATABASE_MAX_CONNS",
    "DEFAULT_DRAIN_TIMEOUT",
    "DEFAULT_MANAGEMENT_PORT",
    "DEFAULT_METRICS_EXPORT_INTERVAL",
    "DEFAULT_PORT",
    "DEFAULT_PROVIDER_WEBHOOK_PORT",
    "DEFAULT_PROVIDER_WEBHOOK_TOLERANCE",
    "DEFAULT_READINESS_DELAY",
    "DEFAULT_SPICEDB_TIMEOUT",
    "DEFAULT_TELEMETRY_FLUSH_TIMEOUT",
    "EVENT_STORE_SUBJECT_KEKS_ENV",
    "EVENT_STORE_SUBJECT_REF_KEY_ENV",
    "IDENTITY_AUDIENCE_KEY",
    "IDENTITY_ISSUER_KEY",
    "IDENTITY_JWKS_FILE_KEY",
    "IDENTITY_SERVICE_ACCOUNTS_KEY",
    "IDENTITY_USER_DELEGATES_KEY",
    "IDENTITY_USER_ISSUER_KEY",
    "IDENTITY_USER_JWKS_FILE_KEY",
    "KIND",
    "LOG_LEVELS",
    "PAYMENTS_ROLE",
    "POD_NAME_ENV",
    "PROVIDER_KINDS",
    "PROVIDER_STRIPE_API_KEY_ENV",
    "PROVIDER_WEBHOOK_SECRET_ENV",
    "SPICEDB_PRESHARED_KEY_ENV",
    "TELEMETRY_PROTOCOLS",
    "AdminSettings",
    "ConfigError",
    "DatabaseSpec",
    "EventStoreSettings",
    "EventStoreSpec",
    "IdentitySettings",
    "LogLevel",
    "LoggingSpec",
    "ManagementSettings",
    "MetricsSpec",
    "ProviderSettings",
    "ProviderSpec",
    "ServerSettings",
    "Settings",
    "ShutdownSettings",
    "Spec",
    "SpiceDBSettings",
    "SpiceDBSpec",
    "TelemetrySettings",
    "TelemetrySpec",
    "TracingSpec",
    "admin_settings",
    "describe",
    "load_admin_settings",
    "load_settings",
    "load_spec",
    "parse_document",
    "parse_duration",
    "parse_service_accounts",
    "parse_spec",
    "parse_user_delegates",
    "settings",
]

# metadata.name файла процесса payments и его service.name по умолчанию.
PAYMENTS_ROLE = "payments"
# service.name операторской команды по умолчанию; роль файла она не проверяет.
ADMIN_PROCESS_NAME = "payments-admin"


@dataclass(frozen=True)
class Settings:
    """Validated configuration of the payments process, grouped by section."""

    server: ServerSettings
    management: ManagementSettings
    shutdown: ShutdownSettings
    identity: IdentitySettings
    spicedb: SpiceDBSettings
    provider: ProviderSettings
    database: database.Config
    event_store: EventStoreSettings
    telemetry: TelemetrySettings


@dataclass(frozen=True)
class AdminSettings:
    """Configuration of ``payments_admin``: the sections and secrets it uses."""

    database: database.Config
    event_store: EventStoreSettings
    telemetry: TelemetrySettings


def settings(spec: Spec) -> Settings:
    """Settings of the payments process: ``spec`` plus its secrets and ``POD_NAME`` from env.

    Raises:
        ConfigError: A secret is missing or invalid, ``POD_NAME`` is empty or the server,
            management and webhook ports are not distinct.
    """
    try:
        result = Settings(
            server=spec.server,
            management=spec.management,
            shutdown=spec.shutdown,
            identity=spec.identity,
            spicedb=SpiceDBSettings.from_spec(spec.spicedb),
            provider=ProviderSettings.from_spec(spec.provider),
            database=database.Config.from_spec(spec.database),
            event_store=EventStoreSettings.from_spec(spec.event_store),
            telemetry=TelemetrySettings.from_spec(
                spec.telemetry, spec.tracing, spec.metrics, spec.logging, PAYMENTS_ROLE
            ),
        )
    except ValidationError as error:
        raise ConfigError(describe(error)) from None
    except ValueError as error:
        raise ConfigError(str(error)) from None
    if result.management.port == result.server.port:
        raise ConfigError(
            f"spec.management.port must differ from spec.server.port {result.server.port}",
        )
    if result.provider.webhook_port in (result.server.port, result.management.port):
        raise ConfigError(
            "spec.provider.webhookPort must differ from spec.server.port and spec.management.port",
        )
    return result


def admin_settings(spec: Spec) -> AdminSettings:
    """Settings of ``payments_admin``: database, event store and logging.

    Raises:
        ConfigError: A secret it uses is missing or invalid, or ``POD_NAME`` is empty.
    """
    try:
        return AdminSettings(
            database=database.Config.from_spec(spec.database),
            event_store=EventStoreSettings.from_spec(spec.event_store),
            telemetry=TelemetrySettings.from_spec(
                spec.telemetry, spec.tracing, spec.metrics, spec.logging, ADMIN_PROCESS_NAME
            ),
        )
    except ValidationError as error:
        raise ConfigError(describe(error)) from None
    except ValueError as error:
        raise ConfigError(str(error)) from None


def load_settings() -> Settings:
    """Load and validate the configuration of the ``payments`` process.

    The file must be the ``payments`` role; a failure stops the process before any port
    is open.

    Raises:
        ConfigError: The file, a secret or ``POD_NAME`` is missing or invalid.
    """
    return settings(load_spec(PAYMENTS_ROLE))


def load_admin_settings() -> AdminSettings:
    """Load the configuration of ``payments_admin`` from the file of its pod.

    The command runs in the pod of a process and checks only ``apiVersion`` and ``kind``.

    Raises:
        ConfigError: The file, a secret it uses or ``POD_NAME`` is missing or invalid.
    """
    return admin_settings(load_spec(None))

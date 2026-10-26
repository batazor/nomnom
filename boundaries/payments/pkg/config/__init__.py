"""Configuration of payments from env; a validation error stops the process."""

from dataclasses import dataclass

from . import database
from .database import (
    DATABASE_MAX_CONNS_ENV,
    DATABASE_URI_ENV,
    DEFAULT_DATABASE_MAX_CONNS,
)
from .duration import parse_duration
from .identity import (
    IDENTITY_AUDIENCE_ENV,
    IDENTITY_ISSUER_ENV,
    IDENTITY_JWKS_FILE_ENV,
    IDENTITY_SERVICE_ACCOUNTS_ENV,
    IDENTITY_USER_DELEGATES_ENV,
    IDENTITY_USER_ISSUER_ENV,
    IDENTITY_USER_JWKS_FILE_ENV,
    IdentitySettings,
    parse_service_accounts,
    parse_user_delegates,
)
from .provider import (
    DEFAULT_PROVIDER_WEBHOOK_PORT,
    DEFAULT_PROVIDER_WEBHOOK_TOLERANCE,
    PROVIDER_KIND_ENV,
    PROVIDER_KINDS,
    PROVIDER_WEBHOOK_PORT_ENV,
    PROVIDER_WEBHOOK_SECRET_ENV,
    PROVIDER_WEBHOOK_TOLERANCE_ENV,
    ProviderSettings,
)
from .runtime import (
    DEFAULT_DRAIN_TIMEOUT,
    DEFAULT_MANAGEMENT_PORT,
    DEFAULT_PORT,
    DEFAULT_READINESS_DELAY,
    DEFAULT_TELEMETRY_FLUSH_TIMEOUT,
    DRAIN_TIMEOUT_ENV,
    MANAGEMENT_PORT_ENV,
    PORT_ENV,
    READINESS_DELAY_ENV,
    TELEMETRY_FLUSH_TIMEOUT_ENV,
    ManagementSettings,
    ServerSettings,
    ShutdownSettings,
)
from .spicedb import (
    DEFAULT_SPICEDB_TIMEOUT,
    SPICEDB_ENDPOINT_ENV,
    SPICEDB_INSECURE_ENV,
    SPICEDB_PRESHARED_KEY_ENV,
    SPICEDB_TIMEOUT_ENV,
    SpiceDBSettings,
)

__all__ = [
    "DATABASE_MAX_CONNS_ENV",
    "DATABASE_URI_ENV",
    "DEFAULT_DATABASE_MAX_CONNS",
    "DEFAULT_DRAIN_TIMEOUT",
    "DEFAULT_MANAGEMENT_PORT",
    "DEFAULT_PORT",
    "DEFAULT_PROVIDER_WEBHOOK_PORT",
    "DEFAULT_PROVIDER_WEBHOOK_TOLERANCE",
    "DEFAULT_READINESS_DELAY",
    "DEFAULT_SPICEDB_TIMEOUT",
    "DEFAULT_TELEMETRY_FLUSH_TIMEOUT",
    "DRAIN_TIMEOUT_ENV",
    "IDENTITY_AUDIENCE_ENV",
    "IDENTITY_ISSUER_ENV",
    "IDENTITY_JWKS_FILE_ENV",
    "IDENTITY_SERVICE_ACCOUNTS_ENV",
    "IDENTITY_USER_DELEGATES_ENV",
    "IDENTITY_USER_ISSUER_ENV",
    "IDENTITY_USER_JWKS_FILE_ENV",
    "MANAGEMENT_PORT_ENV",
    "PORT_ENV",
    "PROVIDER_KINDS",
    "PROVIDER_KIND_ENV",
    "PROVIDER_WEBHOOK_PORT_ENV",
    "PROVIDER_WEBHOOK_SECRET_ENV",
    "PROVIDER_WEBHOOK_TOLERANCE_ENV",
    "READINESS_DELAY_ENV",
    "SPICEDB_ENDPOINT_ENV",
    "SPICEDB_INSECURE_ENV",
    "SPICEDB_PRESHARED_KEY_ENV",
    "SPICEDB_TIMEOUT_ENV",
    "TELEMETRY_FLUSH_TIMEOUT_ENV",
    "IdentitySettings",
    "ManagementSettings",
    "ProviderSettings",
    "ServerSettings",
    "Settings",
    "ShutdownSettings",
    "SpiceDBSettings",
    "load_settings",
    "parse_duration",
    "parse_service_accounts",
    "parse_user_delegates",
]


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


def load_settings() -> Settings:
    """Load and validate the payments configuration from env.

    A failure stops the process before any port is open.

    Raises:
        ValueError: A setting is invalid or the server, management and webhook ports
            are not distinct (pydantic ``ValidationError`` is a ``ValueError``).
    """
    settings = Settings(
        server=ServerSettings(),
        management=ManagementSettings(),
        shutdown=ShutdownSettings(),
        identity=IdentitySettings(),
        spicedb=SpiceDBSettings(),
        provider=ProviderSettings(),
        database=database.Config(),
    )
    if settings.management.port == settings.server.port:
        raise ValueError(
            f"management port must differ from server port {settings.server.port}",
        )
    if settings.provider.webhook_port in (settings.server.port, settings.management.port):
        raise ValueError(
            f"{PROVIDER_WEBHOOK_PORT_ENV} must differ from server and management ports",
        )
    return settings

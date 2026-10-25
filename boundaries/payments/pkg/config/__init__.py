import re
from dataclasses import dataclass
from datetime import timedelta

from pydantic import AliasChoices, Field, field_validator, model_validator
from pydantic_settings import BaseSettings, SettingsConfigDict

DEFAULT_PORT = 8084
PORT_ENV = "SERVER__PORT"

DEFAULT_MANAGEMENT_PORT = 9084
MANAGEMENT_PORT_ENV = "MANAGEMENT__PORT"

DEFAULT_READINESS_DELAY = timedelta(seconds=5)
READINESS_DELAY_ENV = "SHUTDOWN__READINESS_DELAY"

DEFAULT_DRAIN_TIMEOUT = timedelta(seconds=20)
DRAIN_TIMEOUT_ENV = "SHUTDOWN__DRAIN_TIMEOUT"

DEFAULT_TELEMETRY_FLUSH_TIMEOUT = timedelta(seconds=5)
TELEMETRY_FLUSH_TIMEOUT_ENV = "SHUTDOWN__TELEMETRY_FLUSH_TIMEOUT"

IDENTITY_ISSUER_ENV = "IDENTITY__ISSUER"
IDENTITY_AUDIENCE_ENV = "IDENTITY__AUDIENCE"
IDENTITY_JWKS_FILE_ENV = "IDENTITY__JWKS_FILE"
IDENTITY_SERVICE_ACCOUNTS_ENV = "IDENTITY__SERVICE_ACCOUNTS"

SPICEDB_ENDPOINT_ENV = "SPICEDB__ENDPOINT"
SPICEDB_PRESHARED_KEY_ENV = "SPICEDB__PRESHARED_KEY"
SPICEDB_INSECURE_ENV = "SPICEDB__INSECURE"
SPICEDB_TIMEOUT_ENV = "SPICEDB__TIMEOUT"
DEFAULT_SPICEDB_TIMEOUT = timedelta(seconds=2)

DATABASE_URL_ENV = "DATABASE__URL"
DATABASE_MAX_CONNS_ENV = "DATABASE__MAX_CONNS"
DEFAULT_DATABASE_MAX_CONNS = 10

PROVIDER_KIND_ENV = "PROVIDER__KIND"
PROVIDER_WEBHOOK_SECRET_ENV = "PROVIDER__WEBHOOK_SECRET"
PROVIDER_WEBHOOK_TOLERANCE_ENV = "PROVIDER__WEBHOOK_TOLERANCE"
DEFAULT_PROVIDER_WEBHOOK_TOLERANCE = timedelta(minutes=5)
PROVIDER_KINDS = ("unconfigured", "fake")

# Формат длительности общий для всех языков проекта: целое число и единица ms, s или m.
_DURATION_PATTERN = re.compile(r"^(\d+)(ms|s|m)$")
_DURATION_UNITS = {
    "ms": timedelta(milliseconds=1),
    "s": timedelta(seconds=1),
    "m": timedelta(minutes=1),
}


def parse_duration(value: str) -> timedelta:
    match = _DURATION_PATTERN.match(value)
    if match is None:
        raise ValueError(f"invalid duration {value!r}: expected <int>(ms|s|m), e.g. 500ms, 5s, 1m")
    amount, unit = match.groups()
    return int(amount) * _DURATION_UNITS[unit]


def _duration(value: object) -> timedelta:
    # Числа и ISO 8601, которые pydantic принимает для timedelta, не допускаются:
    # единственный внешний формат — строка <int>(ms|s|m).
    if isinstance(value, timedelta):
        return value
    if isinstance(value, str):
        return parse_duration(value.strip())
    raise ValueError(f"invalid duration {value!r}: expected <int>(ms|s|m)")


class ServerSettings(BaseSettings):
    model_config = SettingsConfigDict(env_file=".env", extra="ignore")

    port: int = Field(
        default=DEFAULT_PORT,
        ge=1,
        le=65535,
        validation_alias=AliasChoices(PORT_ENV, "PORT"),
    )


class ManagementSettings(BaseSettings):
    """Технический порт (Kubernetes-имя `metrics`): health отделён от бизнес-порта."""

    model_config = SettingsConfigDict(env_file=".env", extra="ignore")

    port: int = Field(
        default=DEFAULT_MANAGEMENT_PORT,
        ge=1,
        le=65535,
        validation_alias=MANAGEMENT_PORT_ENV,
    )


class ShutdownSettings(BaseSettings):
    model_config = SettingsConfigDict(env_file=".env", extra="ignore")

    # Ожидание распространения not ready до остановки приёма запросов.
    readiness_delay: timedelta = Field(
        default=DEFAULT_READINESS_DELAY,
        validation_alias=READINESS_DELAY_ENV,
    )
    # Deadline на завершение начатой работы.
    drain_timeout: timedelta = Field(
        default=DEFAULT_DRAIN_TIMEOUT,
        validation_alias=DRAIN_TIMEOUT_ENV,
    )
    # Deadline на flush и shutdown telemetry providers после drain.
    telemetry_flush_timeout: timedelta = Field(
        default=DEFAULT_TELEMETRY_FLUSH_TIMEOUT,
        validation_alias=TELEMETRY_FLUSH_TIMEOUT_ENV,
    )

    @field_validator("readiness_delay", "drain_timeout", "telemetry_flush_timeout", mode="before")
    @classmethod
    def _parse_duration(cls, value: object) -> timedelta:
        return _duration(value)

    @field_validator("readiness_delay")
    @classmethod
    def _readiness_delay_not_negative(cls, value: timedelta) -> timedelta:
        if value < timedelta(0):
            raise ValueError("shutdown readiness delay must not be negative")
        return value

    @field_validator("drain_timeout")
    @classmethod
    def _drain_timeout_positive(cls, value: timedelta) -> timedelta:
        if value <= timedelta(0):
            raise ValueError("shutdown drain timeout must be positive")
        return value

    @field_validator("telemetry_flush_timeout")
    @classmethod
    def _telemetry_flush_timeout_positive(cls, value: timedelta) -> timedelta:
        if value <= timedelta(0):
            raise ValueError("shutdown telemetry flush timeout must be positive")
        return value


# Сегмент имени Kubernetes (namespace, ServiceAccount): без разделителей ``/``, ``:``, ``=``.
_SERVICE_ACCOUNT_ENTRY = re.compile(r"^([^/:=\s]+)/([^/:=\s]+)=(\S+)$")


def parse_service_accounts(value: str) -> dict[str, str]:
    """Разбирает allowlist ``namespace/serviceaccount=service-name`` через запятую.

    Возвращает отображение ``system:serviceaccount:<ns>:<sa>`` → имя сервиса (ADR 27).
    """
    accounts: dict[str, str] = {}
    if not value.strip():
        return accounts
    for raw in value.split(","):
        entry = raw.strip()
        match = _SERVICE_ACCOUNT_ENTRY.match(entry)
        if match is None:
            raise ValueError(
                f"invalid {IDENTITY_SERVICE_ACCOUNTS_ENV} entry {entry!r}: "
                "expected namespace/serviceaccount=service-name",
            )
        namespace, account, service = match.groups()
        subject = f"system:serviceaccount:{namespace}:{account}"
        if subject in accounts:
            raise ValueError(
                f"duplicate {IDENTITY_SERVICE_ACCOUNTS_ENV} entry for {namespace}/{account}",
            )
        accounts[subject] = service
    return accounts


class IdentitySettings(BaseSettings):
    """Проверка service identity по projected ServiceAccount token (ADR 27).

    Пока issuer, audience или JWKS не заданы, вызовы отклоняются (fail closed).
    """

    model_config = SettingsConfigDict(env_file=".env", extra="ignore")

    issuer: str = Field(default="", validation_alias=IDENTITY_ISSUER_ENV)
    audience: str = Field(default="", validation_alias=IDENTITY_AUDIENCE_ENV)
    jwks_file: str = Field(default="", validation_alias=IDENTITY_JWKS_FILE_ENV)
    # ``namespace/serviceaccount=service-name`` через запятую.
    service_accounts: str = Field(default="", validation_alias=IDENTITY_SERVICE_ACCOUNTS_ENV)

    @field_validator("issuer", "audience", "jwks_file", "service_accounts")
    @classmethod
    def _strip(cls, value: str) -> str:
        return value.strip()

    @model_validator(mode="after")
    def _validate_allowlist(self) -> "IdentitySettings":
        accounts = parse_service_accounts(self.service_accounts)
        if self.issuer and not accounts:
            raise ValueError(
                f"{IDENTITY_SERVICE_ACCOUNTS_ENV} must not be empty when "
                f"{IDENTITY_ISSUER_ENV} is set",
            )
        return self

    @property
    def enabled(self) -> bool:
        return bool(self.issuer and self.audience and self.jwks_file)

    @property
    def service_account_map(self) -> dict[str, str]:
        return parse_service_accounts(self.service_accounts)


class SpiceDBSettings(BaseSettings):
    """Подключение к SpiceDB (ADR 18); пустой endpoint — запрет всех permissions."""

    model_config = SettingsConfigDict(env_file=".env", extra="ignore")

    endpoint: str = Field(default="", validation_alias=SPICEDB_ENDPOINT_ENV)
    # Bearer preshared key; в кластере приходит из Secret, в values не хранится.
    preshared_key: str = Field(default="", validation_alias=SPICEDB_PRESHARED_KEY_ENV, repr=False)
    # Plaintext gRPC только для kind и локального запуска.
    insecure: bool = Field(default=False, validation_alias=SPICEDB_INSECURE_ENV)
    # Deadline одного CheckPermission.
    timeout: timedelta = Field(
        default=DEFAULT_SPICEDB_TIMEOUT, validation_alias=SPICEDB_TIMEOUT_ENV
    )

    @field_validator("endpoint", "preshared_key")
    @classmethod
    def _strip(cls, value: str) -> str:
        return value.strip()

    @field_validator("timeout", mode="before")
    @classmethod
    def _parse_duration(cls, value: object) -> timedelta:
        return _duration(value)

    @field_validator("timeout")
    @classmethod
    def _timeout_positive(cls, value: timedelta) -> timedelta:
        if value <= timedelta(0):
            raise ValueError("SpiceDB timeout must be positive")
        return value

    @model_validator(mode="after")
    def _require_key(self) -> "SpiceDBSettings":
        if self.endpoint and not self.preshared_key:
            raise ValueError(
                f"{SPICEDB_PRESHARED_KEY_ENV} must not be empty when {SPICEDB_ENDPOINT_ENV} is set",
            )
        return self

    @property
    def enabled(self) -> bool:
        return bool(self.endpoint)


class ProviderSettings(BaseSettings):
    """Платёжный провайдер; ``unconfigured`` — каждая операция ``UNAVAILABLE``.

    ``fake`` — детерминированный sandbox в памяти процесса, без реальных денег.
    """

    model_config = SettingsConfigDict(env_file=".env", extra="ignore")

    kind: str = Field(default="unconfigured", validation_alias=PROVIDER_KIND_ENV)
    # Общий секрет HMAC подписи webhooks; в кластере приходит из Secret.
    webhook_secret: str = Field(
        default="", validation_alias=PROVIDER_WEBHOOK_SECRET_ENV, repr=False
    )
    # Допустимое расхождение времени подписи webhook и часов payments.
    webhook_tolerance: timedelta = Field(
        default=DEFAULT_PROVIDER_WEBHOOK_TOLERANCE,
        validation_alias=PROVIDER_WEBHOOK_TOLERANCE_ENV,
    )

    @field_validator("kind", "webhook_secret")
    @classmethod
    def _strip(cls, value: str) -> str:
        return value.strip()

    @field_validator("kind")
    @classmethod
    def _known_kind(cls, value: str) -> str:
        if value not in PROVIDER_KINDS:
            raise ValueError(f"{PROVIDER_KIND_ENV} must be one of {PROVIDER_KINDS}, got {value!r}")
        return value

    @field_validator("webhook_tolerance", mode="before")
    @classmethod
    def _parse_duration(cls, value: object) -> timedelta:
        return _duration(value)

    @field_validator("webhook_tolerance")
    @classmethod
    def _tolerance_positive(cls, value: timedelta) -> timedelta:
        if value <= timedelta(0):
            raise ValueError("provider webhook tolerance must be positive")
        return value


class DatabaseSettings(BaseSettings):
    """Подключение к PostgreSQL payments (ADR 8, локальный ADR 1).

    DSN обязателен: пустое значение завершает процесс на старте. В кластере приходит
    из Secret и в values не хранится.
    """

    model_config = SettingsConfigDict(env_file=".env", extra="ignore")

    url: str = Field(default="", validation_alias=DATABASE_URL_ENV, repr=False)
    max_conns: int = Field(
        default=DEFAULT_DATABASE_MAX_CONNS, ge=1, validation_alias=DATABASE_MAX_CONNS_ENV
    )

    @field_validator("url")
    @classmethod
    def _require_url(cls, value: str) -> str:
        value = value.strip()
        if not value:
            raise ValueError(f"{DATABASE_URL_ENV} must not be empty")
        return value


@dataclass(frozen=True)
class Settings:
    server: ServerSettings
    management: ManagementSettings
    shutdown: ShutdownSettings
    identity: IdentitySettings
    spicedb: SpiceDBSettings
    provider: ProviderSettings
    database: DatabaseSettings


def load_settings() -> Settings:
    """Загружает и валидирует конфигурацию; ошибка завершает процесс до открытия портов."""
    settings = Settings(
        server=ServerSettings(),
        management=ManagementSettings(),
        shutdown=ShutdownSettings(),
        identity=IdentitySettings(),
        spicedb=SpiceDBSettings(),
        provider=ProviderSettings(),
        database=DatabaseSettings(),
    )
    if settings.management.port == settings.server.port:
        raise ValueError(
            f"management port must differ from server port {settings.server.port}",
        )
    return settings

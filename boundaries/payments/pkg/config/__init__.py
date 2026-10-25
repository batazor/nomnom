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

IDENTITY_ISSUER_ENV = "IDENTITY__ISSUER"
IDENTITY_AUDIENCE_ENV = "IDENTITY__AUDIENCE"
IDENTITY_JWKS_FILE_ENV = "IDENTITY__JWKS_FILE"
IDENTITY_SERVICE_ACCOUNTS_ENV = "IDENTITY__SERVICE_ACCOUNTS"

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

    @field_validator("readiness_delay", "drain_timeout", mode="before")
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


@dataclass(frozen=True)
class Settings:
    server: ServerSettings
    management: ManagementSettings
    shutdown: ShutdownSettings
    identity: IdentitySettings


def load_settings() -> Settings:
    """Загружает и валидирует конфигурацию; ошибка завершает процесс до открытия портов."""
    settings = Settings(
        server=ServerSettings(),
        management=ManagementSettings(),
        shutdown=ShutdownSettings(),
        identity=IdentitySettings(),
    )
    if settings.management.port == settings.server.port:
        raise ValueError(
            f"management port must differ from server port {settings.server.port}",
        )
    return settings

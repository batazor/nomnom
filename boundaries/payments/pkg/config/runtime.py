"""Порты процесса и сроки graceful shutdown."""

from datetime import timedelta

from pydantic import AliasChoices, Field, field_validator
from pydantic_settings import BaseSettings, SettingsConfigDict

from .duration import _duration

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

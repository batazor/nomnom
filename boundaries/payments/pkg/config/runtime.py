"""Process ports and graceful shutdown timings."""

from datetime import timedelta

from pydantic import Field, field_validator

from .duration import _duration
from .section import Section

DEFAULT_PORT = 8084
DEFAULT_MANAGEMENT_PORT = 9084
DEFAULT_READINESS_DELAY = timedelta(seconds=5)
DEFAULT_DRAIN_TIMEOUT = timedelta(seconds=20)
DEFAULT_TELEMETRY_FLUSH_TIMEOUT = timedelta(seconds=5)


class ServerSettings(Section):
    """Business gRPC port (``spec.server``)."""

    port: int = Field(default=DEFAULT_PORT, ge=1, le=65535)


class ManagementSettings(Section):
    """Management port (Kubernetes name ``metrics``): health is kept off the business port."""

    port: int = Field(default=DEFAULT_MANAGEMENT_PORT, ge=1, le=65535)


class ShutdownSettings(Section):
    """Graceful shutdown timings: readiness delay, drain deadline and telemetry flush."""

    # Ожидание распространения not ready до остановки приёма запросов.
    readiness_delay: timedelta = DEFAULT_READINESS_DELAY
    # Deadline на завершение начатой работы.
    drain_timeout: timedelta = DEFAULT_DRAIN_TIMEOUT
    # Deadline на flush и shutdown telemetry providers после drain.
    telemetry_flush_timeout: timedelta = DEFAULT_TELEMETRY_FLUSH_TIMEOUT

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

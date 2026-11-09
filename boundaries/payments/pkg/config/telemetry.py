"""OpenTelemetry and logging of the process (ADR 9).

Sections ``spec.telemetry``, ``tracing``, ``metrics`` and ``logging``; ``service.instance.id``
is the pod name from ``POD_NAME``.
"""

import os
from collections.abc import Mapping
from dataclasses import dataclass
from datetime import timedelta
from typing import Literal

from pydantic import Field, field_validator

from .duration import _duration
from .section import Section

POD_NAME_ENV = "POD_NAME"
TELEMETRY_PROTOCOLS = ("grpc", "http/protobuf")
LOG_LEVELS = ("debug", "info", "warn", "error")
# Идентичность процесса задают serviceName и POD_NAME, а не resourceAttributes.
RESERVED_RESOURCE_ATTRIBUTES = ("service.name", "service.instance.id")
DEFAULT_TELEMETRY_PROTOCOL = "grpc"
DEFAULT_METRICS_EXPORT_INTERVAL = timedelta(seconds=60)
DEFAULT_LOG_LEVEL = "info"

type TelemetryProtocol = Literal["grpc", "http/protobuf"]
type LogLevel = Literal["debug", "info", "warn", "error"]


class TelemetrySpec(Section):
    """``spec.telemetry``: OTLP collector, protocol and resource of the process."""

    # URL коллектора OTLP; значения по умолчанию нет: обязателен при включённом экспорте.
    endpoint: str = ""
    protocol: TelemetryProtocol = DEFAULT_TELEMETRY_PROTOCOL
    # service.name; без значения — имя процесса.
    service_name: str | None = Field(default=None, min_length=1)
    resource_attributes: dict[str, str] = Field(default_factory=dict)

    @field_validator("endpoint")
    @classmethod
    def _strip(cls, value: str) -> str:
        return value.strip()

    @field_validator("resource_attributes")
    @classmethod
    def _no_identity(cls, value: dict[str, str]) -> dict[str, str]:
        reserved = sorted(set(RESERVED_RESOURCE_ATTRIBUTES) & value.keys())
        if reserved:
            raise ValueError(
                f"must not set {', '.join(reserved)}: the process sets them "
                "(spec.telemetry.serviceName, env POD_NAME)"
            )
        return value


class TracingSpec(Section):
    """``spec.tracing``: spans are exported only when enabled."""

    enabled: bool = False


class MetricsSpec(Section):
    """``spec.metrics``: metrics are exported only when enabled, every ``export_interval``."""

    enabled: bool = False
    export_interval: timedelta = DEFAULT_METRICS_EXPORT_INTERVAL

    @field_validator("export_interval", mode="before")
    @classmethod
    def _parse_duration(cls, value: object) -> timedelta:
        return _duration(value)

    @field_validator("export_interval")
    @classmethod
    def _positive(cls, value: timedelta) -> timedelta:
        if value <= timedelta(0):
            raise ValueError("metrics export interval must be positive")
        return value


class LoggingSpec(Section):
    """``spec.logging``: minimum level of the JSON logs on stdout."""

    level: LogLevel = DEFAULT_LOG_LEVEL


@dataclass(frozen=True)
class TelemetrySettings:
    """Telemetry of one process: the sections of the file plus ``service.instance.id``."""

    endpoint: str
    protocol: TelemetryProtocol
    service_name: str
    instance_id: str
    resource_attributes: Mapping[str, str]
    tracing_enabled: bool
    metrics_enabled: bool
    metrics_export_interval: timedelta
    log_level: LogLevel

    @classmethod
    def from_spec(
        cls,
        telemetry: TelemetrySpec,
        tracing: TracingSpec,
        metrics: MetricsSpec,
        logging: LoggingSpec,
        process_name: str,
    ) -> TelemetrySettings:
        """Settings of the process ``process_name``, the default ``service.name``.

        Raises:
            ValueError: ``POD_NAME`` is empty.
        """
        instance_id = os.environ.get(POD_NAME_ENV)
        if instance_id is None or not instance_id.strip():
            raise ValueError(f"{POD_NAME_ENV} must not be empty: it is service.instance.id")
        return cls(
            endpoint=telemetry.endpoint,
            protocol=telemetry.protocol,
            service_name=process_name if telemetry.service_name is None else telemetry.service_name,
            instance_id=instance_id.strip(),
            resource_attributes=dict(telemetry.resource_attributes),
            tracing_enabled=tracing.enabled,
            metrics_enabled=metrics.enabled,
            metrics_export_interval=metrics.export_interval,
            log_level=logging.level,
        )

"""Конфигурация payments-relay (ADR 31): Kafka и polling Outbox."""

from dataclasses import dataclass
from datetime import timedelta

from pydantic import Field, field_validator
from pydantic_settings import BaseSettings, SettingsConfigDict

from .database import DatabaseSettings
from .duration import _duration
from .runtime import ManagementSettings, ShutdownSettings

KAFKA_BROKERS_ENV = "KAFKA__BROKERS"
KAFKA_CLIENT_ID_ENV = "KAFKA__CLIENT_ID"
DEFAULT_KAFKA_CLIENT_ID = "payments-relay"

OUTBOX_POLL_INTERVAL_ENV = "OUTBOX__POLL_INTERVAL"
DEFAULT_OUTBOX_POLL_INTERVAL = timedelta(seconds=1)
OUTBOX_BATCH_SIZE_ENV = "OUTBOX__BATCH_SIZE"
DEFAULT_OUTBOX_BATCH_SIZE = 100
OUTBOX_RETENTION_ENV = "OUTBOX__RETENTION"
# 7 дней; общий формат длительности не знает часов и дней: в env — 10080m.
DEFAULT_OUTBOX_RETENTION = timedelta(days=7)


class KafkaSettings(BaseSettings):
    """Kafka relay payments (ADR 31). Брокеры обязательны: основной процесс Kafka не
    использует, а relay без них завершается на старте."""

    model_config = SettingsConfigDict(env_file=".env", extra="ignore")

    # host:port через запятую (bootstrap.servers librdkafka).
    brokers: str = Field(default="", validation_alias=KAFKA_BROKERS_ENV)
    client_id: str = Field(default=DEFAULT_KAFKA_CLIENT_ID, validation_alias=KAFKA_CLIENT_ID_ENV)

    @field_validator("brokers")
    @classmethod
    def _require_brokers(cls, value: str) -> str:
        brokers = ",".join(b.strip() for b in value.split(",") if b.strip())
        if not brokers:
            raise ValueError(f"{KAFKA_BROKERS_ENV} must not be empty")
        return brokers

    @field_validator("client_id")
    @classmethod
    def _require_client_id(cls, value: str) -> str:
        value = value.strip()
        if not value:
            raise ValueError(f"{KAFKA_CLIENT_ID_ENV} must not be empty")
        return value


class OutboxSettings(BaseSettings):
    """Polling relay Outbox (ADR 31)."""

    model_config = SettingsConfigDict(env_file=".env", extra="ignore")

    poll_interval: timedelta = Field(
        default=DEFAULT_OUTBOX_POLL_INTERVAL, validation_alias=OUTBOX_POLL_INTERVAL_ENV
    )
    batch_size: int = Field(
        default=DEFAULT_OUTBOX_BATCH_SIZE, ge=1, le=10000, validation_alias=OUTBOX_BATCH_SIZE_ENV
    )
    # Опубликованные записи старше retention удаляются; неопубликованные — никогда.
    retention: timedelta = Field(
        default=DEFAULT_OUTBOX_RETENTION, validation_alias=OUTBOX_RETENTION_ENV
    )

    @field_validator("poll_interval", "retention", mode="before")
    @classmethod
    def _parse_duration(cls, value: object) -> timedelta:
        return _duration(value)

    @field_validator("poll_interval", "retention")
    @classmethod
    def _positive(cls, value: timedelta) -> timedelta:
        if value <= timedelta(0):
            raise ValueError("outbox durations must be positive")
        return value


@dataclass(frozen=True)
class RelaySettings:
    management: ManagementSettings
    shutdown: ShutdownSettings
    database: DatabaseSettings
    kafka: KafkaSettings
    outbox: OutboxSettings


def load_relay_settings() -> RelaySettings:
    """Конфигурация payments-relay; пустые DATABASE__URL или KAFKA__BROKERS — ошибка."""
    return RelaySettings(
        management=ManagementSettings(),
        shutdown=ShutdownSettings(),
        database=DatabaseSettings(),
        kafka=KafkaSettings(),
        outbox=OutboxSettings(),
    )

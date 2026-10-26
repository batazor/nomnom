"""Connection to the payments PostgreSQL database and the limits of its process pool."""

from datetime import timedelta
from typing import Self

from pydantic import Field, field_validator, model_validator
from pydantic_settings import BaseSettings, SettingsConfigDict

from .duration import _duration

DATABASE_URL_ENV = "DATABASE__URL"
DATABASE_MAX_CONNS_ENV = "DATABASE__MAX_CONNS"
DATABASE_MIN_CONNS_ENV = "DATABASE__MIN_CONNS"
DATABASE_MAX_CONN_LIFETIME_ENV = "DATABASE__MAX_CONN_LIFETIME"
DATABASE_MAX_CONN_IDLE_TIME_ENV = "DATABASE__MAX_CONN_IDLE_TIME"
DATABASE_CONNECT_TIMEOUT_ENV = "DATABASE__CONNECT_TIMEOUT"
DATABASE_ACQUIRE_TIMEOUT_ENV = "DATABASE__ACQUIRE_TIMEOUT"

# Значения по умолчанию рассчитаны на API-процесс за пулером в session mode; Helm задаёт
# max_conns под конкурентность каждого процесса.
DEFAULT_DATABASE_MAX_CONNS = 10
DEFAULT_DATABASE_MIN_CONNS = 0
# psycopg_pool сам добавляет jitter: соединение закрывается на 0-5% раньше max_lifetime.
DEFAULT_DATABASE_MAX_CONN_LIFETIME = timedelta(minutes=30)
DEFAULT_DATABASE_MAX_CONN_IDLE_TIME = timedelta(minutes=5)
DEFAULT_DATABASE_CONNECT_TIMEOUT = timedelta(seconds=5)
DEFAULT_DATABASE_ACQUIRE_TIMEOUT = timedelta(seconds=5)
# Простаивающие соединения пула держат server-соединения пулера в session mode.
MAX_DATABASE_MIN_CONNS = 1


class Config(BaseSettings):
    """Connection to the payments PostgreSQL database (ADR 8, local ADR 1).

    The DSN is required: an empty value stops the process at startup. In the cluster it
    comes from a Secret and is never stored in values. Pool limits are validated at
    startup as well: every pooled connection pins a server connection of the
    session-mode pooler.
    """

    model_config = SettingsConfigDict(env_file=".env", extra="ignore")

    url: str = Field(default="", validation_alias=DATABASE_URL_ENV, repr=False)
    max_conns: int = Field(
        default=DEFAULT_DATABASE_MAX_CONNS, ge=1, validation_alias=DATABASE_MAX_CONNS_ENV
    )
    min_conns: int = Field(
        default=DEFAULT_DATABASE_MIN_CONNS,
        ge=0,
        le=MAX_DATABASE_MIN_CONNS,
        validation_alias=DATABASE_MIN_CONNS_ENV,
    )
    max_conn_lifetime: timedelta = Field(
        default=DEFAULT_DATABASE_MAX_CONN_LIFETIME, validation_alias=DATABASE_MAX_CONN_LIFETIME_ENV
    )
    max_conn_idle_time: timedelta = Field(
        default=DEFAULT_DATABASE_MAX_CONN_IDLE_TIME,
        validation_alias=DATABASE_MAX_CONN_IDLE_TIME_ENV,
    )
    # connect_timeout libpq: целые секунды, округляются вверх.
    connect_timeout: timedelta = Field(
        default=DEFAULT_DATABASE_CONNECT_TIMEOUT, validation_alias=DATABASE_CONNECT_TIMEOUT_ENV
    )
    # Ожидание свободного соединения пула; по истечении запрос получает PoolTimeout.
    acquire_timeout: timedelta = Field(
        default=DEFAULT_DATABASE_ACQUIRE_TIMEOUT, validation_alias=DATABASE_ACQUIRE_TIMEOUT_ENV
    )

    @field_validator("url")
    @classmethod
    def _require_url(cls, value: str) -> str:
        value = value.strip()
        if not value:
            raise ValueError(f"{DATABASE_URL_ENV} must not be empty")
        return value

    @field_validator(
        "max_conn_lifetime",
        "max_conn_idle_time",
        "connect_timeout",
        "acquire_timeout",
        mode="before",
    )
    @classmethod
    def _parse_duration(cls, value: object) -> timedelta:
        return _duration(value)

    @field_validator(
        "max_conn_lifetime", "max_conn_idle_time", "connect_timeout", "acquire_timeout"
    )
    @classmethod
    def _positive(cls, value: timedelta) -> timedelta:
        if value <= timedelta(0):
            raise ValueError("database pool durations must be positive")
        return value

    @model_validator(mode="after")
    def _min_within_max(self) -> Self:
        if self.min_conns > self.max_conns:
            raise ValueError(
                f"{DATABASE_MIN_CONNS_ENV} ({self.min_conns}) must not exceed "
                f"{DATABASE_MAX_CONNS_ENV} ({self.max_conns})"
            )
        return self

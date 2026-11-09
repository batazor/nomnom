"""Connection to the payments PostgreSQL database and the limits of its process pool."""

from datetime import timedelta
from typing import Self

from pydantic import Field, field_validator, model_validator
from pydantic_settings import BaseSettings, SettingsConfigDict

from .duration import _duration
from .section import Section

DATABASE_URI_ENV = "DATABASE__URI"

# Префиксы connection URI libpq; key=value и другие схемы не принимаются.
DATABASE_URI_SCHEMES = ("postgresql://", "postgres://")

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


class DatabaseSpec(Section):
    """``spec.database``: limits of the process pool, validated at startup.

    Every pooled connection pins a server connection of the session-mode pooler.
    """

    max_conns: int = Field(default=DEFAULT_DATABASE_MAX_CONNS, ge=1)
    min_conns: int = Field(default=DEFAULT_DATABASE_MIN_CONNS, ge=0, le=MAX_DATABASE_MIN_CONNS)
    max_conn_lifetime: timedelta = DEFAULT_DATABASE_MAX_CONN_LIFETIME
    max_conn_idle_time: timedelta = DEFAULT_DATABASE_MAX_CONN_IDLE_TIME
    # connect_timeout libpq: целые секунды, округляются вверх.
    connect_timeout: timedelta = DEFAULT_DATABASE_CONNECT_TIMEOUT
    # Ожидание свободного соединения пула; по истечении запрос получает PoolTimeout.
    acquire_timeout: timedelta = DEFAULT_DATABASE_ACQUIRE_TIMEOUT

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
                f"spec.database.minConns ({self.min_conns}) must not exceed "
                f"spec.database.maxConns ({self.max_conns})"
            )
        return self


class DatabaseSecrets(BaseSettings):
    """Connection URI of the database: only from env, in the cluster from a Secret.

    The URI is required and must be a PostgreSQL connection URI (``postgresql://`` or
    ``postgres://``); an empty value, the libpq ``key=value`` form or another scheme stops
    the process at startup.
    """

    model_config = SettingsConfigDict(extra="ignore")

    uri: str = Field(default="", validation_alias=DATABASE_URI_ENV, repr=False)

    @field_validator("uri")
    @classmethod
    def _require_uri(cls, value: str) -> str:
        value = value.strip()
        if not value:
            raise ValueError(f"{DATABASE_URI_ENV} must not be empty")
        if not value.startswith(DATABASE_URI_SCHEMES):
            raise ValueError(
                f"{DATABASE_URI_ENV} must be a PostgreSQL connection URI "
                "(postgresql://user@host:port/dbname); the libpq key=value form and other "
                "schemes are not accepted"
            )
        return value


class Config(DatabaseSpec):
    """Connection to the payments PostgreSQL database (ADR 8, local ADR 1).

    ``spec.database`` pool limits plus the connection URI of ``DATABASE__URI``.
    """

    uri: str = Field(repr=False)

    @classmethod
    def from_spec(cls, spec: DatabaseSpec) -> Config:
        """``spec.database`` with the connection URI of the environment."""
        return cls(**spec.model_dump(), uri=DatabaseSecrets().uri)

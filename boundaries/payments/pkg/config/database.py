"""Подключение к PostgreSQL payments."""

from pydantic import Field, field_validator
from pydantic_settings import BaseSettings, SettingsConfigDict

DATABASE_URL_ENV = "DATABASE__URL"
DATABASE_MAX_CONNS_ENV = "DATABASE__MAX_CONNS"
DEFAULT_DATABASE_MAX_CONNS = 10


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

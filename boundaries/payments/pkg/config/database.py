"""Connection to the payments PostgreSQL database."""

from pydantic import Field, field_validator
from pydantic_settings import BaseSettings, SettingsConfigDict

DATABASE_URL_ENV = "DATABASE__URL"
DATABASE_MAX_CONNS_ENV = "DATABASE__MAX_CONNS"
DEFAULT_DATABASE_MAX_CONNS = 10


class DatabaseSettings(BaseSettings):
    """Connection to the payments PostgreSQL database (ADR 8, local ADR 1).

    The DSN is required: an empty value stops the process at startup. In the cluster it
    comes from a Secret and is never stored in values.
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

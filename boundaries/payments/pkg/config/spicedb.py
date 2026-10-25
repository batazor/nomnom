"""Подключение к SpiceDB (ADR 18)."""

from datetime import timedelta

from pydantic import Field, field_validator, model_validator
from pydantic_settings import BaseSettings, SettingsConfigDict

from .duration import _duration

SPICEDB_ENDPOINT_ENV = "SPICEDB__ENDPOINT"
SPICEDB_PRESHARED_KEY_ENV = "SPICEDB__PRESHARED_KEY"
SPICEDB_INSECURE_ENV = "SPICEDB__INSECURE"
SPICEDB_TIMEOUT_ENV = "SPICEDB__TIMEOUT"
DEFAULT_SPICEDB_TIMEOUT = timedelta(seconds=2)


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

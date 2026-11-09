"""Connection to SpiceDB (ADR 18)."""

from datetime import timedelta

from pydantic import Field, field_validator, model_validator
from pydantic_settings import BaseSettings, SettingsConfigDict

from .duration import _duration
from .section import Section

SPICEDB_PRESHARED_KEY_ENV = "SPICEDB__PRESHARED_KEY"
DEFAULT_SPICEDB_TIMEOUT = timedelta(seconds=2)


class SpiceDBSpec(Section):
    """``spec.spicedb``: endpoint, transport and deadline; the key comes from env."""

    endpoint: str = ""
    # Plaintext gRPC только для kind и локального запуска.
    insecure: bool = False
    # Deadline одного CheckPermission.
    timeout: timedelta = DEFAULT_SPICEDB_TIMEOUT

    @field_validator("endpoint")
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


class SpiceDBSecrets(BaseSettings):
    """Bearer preshared key of SpiceDB: only from env, in the cluster from a Secret."""

    model_config = SettingsConfigDict(extra="ignore")

    preshared_key: str = Field(default="", validation_alias=SPICEDB_PRESHARED_KEY_ENV, repr=False)

    @field_validator("preshared_key")
    @classmethod
    def _strip(cls, value: str) -> str:
        return value.strip()


class SpiceDBSettings(SpiceDBSpec):
    """Connection to SpiceDB (ADR 18); an empty endpoint denies every permission."""

    preshared_key: str = Field(default="", repr=False)

    @classmethod
    def from_spec(cls, spec: SpiceDBSpec) -> SpiceDBSettings:
        """``spec.spicedb`` with the preshared key of the environment."""
        return cls(**spec.model_dump(), preshared_key=SpiceDBSecrets().preshared_key)

    @model_validator(mode="after")
    def _require_key(self) -> SpiceDBSettings:
        if self.endpoint and not self.preshared_key:
            raise ValueError(
                f"{SPICEDB_PRESHARED_KEY_ENV} must not be empty when spec.spicedb.endpoint is set",
            )
        return self

    @property
    def enabled(self) -> bool:
        """Whether a SpiceDB endpoint is configured."""
        return bool(self.endpoint)

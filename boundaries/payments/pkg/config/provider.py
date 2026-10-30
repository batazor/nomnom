"""Payment provider and the intake of its webhooks."""

from datetime import timedelta

from pydantic import Field, field_validator, model_validator
from pydantic_settings import BaseSettings, SettingsConfigDict

from .duration import _duration

PROVIDER_KIND_ENV = "PROVIDER__KIND"
PROVIDER_WEBHOOK_SECRET_ENV = "PROVIDER__WEBHOOK_SECRET"  # noqa: S105 - env var name, not a secret
PROVIDER_WEBHOOK_TOLERANCE_ENV = "PROVIDER__WEBHOOK_TOLERANCE"
DEFAULT_PROVIDER_WEBHOOK_TOLERANCE = timedelta(minutes=5)
PROVIDER_KINDS = ("fake", "stripe")
PROVIDER_STRIPE_API_KEY_ENV = "PROVIDER__STRIPE_API_KEY"
PROVIDER_STRIPE_TIMEOUT_ENV = "PROVIDER__STRIPE_TIMEOUT"
DEFAULT_PROVIDER_STRIPE_TIMEOUT = timedelta(seconds=10)
STRIPE_TEST_KEY_PREFIX = "sk_test_"
PROVIDER_WEBHOOK_PORT_ENV = "PROVIDER__WEBHOOK_PORT"
DEFAULT_PROVIDER_WEBHOOK_PORT = 8085


class ProviderSettings(BaseSettings):
    """Payment provider selection, required by the ``payments`` process.

    ``fake`` is a deterministic in-process sandbox that moves no real money. ``stripe`` is
    the Stripe test mode: only a test key is accepted, so it moves no real money either.
    """

    model_config = SettingsConfigDict(env_file=".env", extra="ignore")

    kind: str = Field(default="", validation_alias=PROVIDER_KIND_ENV)
    # Общий секрет HMAC подписи webhooks; в кластере приходит из Secret.
    webhook_secret: str = Field(
        default="", validation_alias=PROVIDER_WEBHOOK_SECRET_ENV, repr=False
    )
    # Секретный ключ Stripe test mode; обязателен при PROVIDER__KIND=stripe, приходит из Secret.
    stripe_api_key: str = Field(
        default="", validation_alias=PROVIDER_STRIPE_API_KEY_ENV, repr=False
    )
    # Deadline одного запроса к Stripe: меньше таймаута Activity orders (30s).
    stripe_timeout: timedelta = Field(
        default=DEFAULT_PROVIDER_STRIPE_TIMEOUT,
        validation_alias=PROVIDER_STRIPE_TIMEOUT_ENV,
    )
    # HTTP-порт приёма webhooks провайдера, отдельный от gRPC и management.
    webhook_port: int = Field(
        default=DEFAULT_PROVIDER_WEBHOOK_PORT,
        ge=1,
        le=65535,
        validation_alias=PROVIDER_WEBHOOK_PORT_ENV,
    )
    # Допустимое расхождение времени подписи webhook и часов payments.
    webhook_tolerance: timedelta = Field(
        default=DEFAULT_PROVIDER_WEBHOOK_TOLERANCE,
        validation_alias=PROVIDER_WEBHOOK_TOLERANCE_ENV,
    )

    @field_validator("kind", "webhook_secret", "stripe_api_key")
    @classmethod
    def _strip(cls, value: str) -> str:
        return value.strip()

    @field_validator("kind")
    @classmethod
    def _known_kind(cls, value: str) -> str:
        if value and value not in PROVIDER_KINDS:
            raise ValueError(f"{PROVIDER_KIND_ENV} must be one of {PROVIDER_KINDS}, got {value!r}")
        return value

    @field_validator("webhook_tolerance", "stripe_timeout", mode="before")
    @classmethod
    def _parse_duration(cls, value: object) -> timedelta:
        return _duration(value)

    @field_validator("stripe_timeout")
    @classmethod
    def _stripe_timeout_positive(cls, value: timedelta) -> timedelta:
        if value <= timedelta(0):
            raise ValueError("stripe timeout must be positive")
        return value

    @model_validator(mode="after")
    def _stripe_test_key(self) -> ProviderSettings:
        if self.kind == "stripe" and not self.stripe_api_key.startswith(STRIPE_TEST_KEY_PREFIX):
            raise ValueError(
                f"{PROVIDER_STRIPE_API_KEY_ENV} must be a test key ({STRIPE_TEST_KEY_PREFIX}...) "
                "with PROVIDER__KIND=stripe"
            )
        return self

    @field_validator("webhook_tolerance")
    @classmethod
    def _tolerance_positive(cls, value: timedelta) -> timedelta:
        if value <= timedelta(0):
            raise ValueError("provider webhook tolerance must be positive")
        return value

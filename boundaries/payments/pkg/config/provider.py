"""Payment provider and the intake of its webhooks."""

from datetime import timedelta

from pydantic import Field, field_validator, model_validator
from pydantic_settings import BaseSettings, SettingsConfigDict

from .duration import _duration
from .section import Section

PROVIDER_KIND_KEY = "spec.provider.kind"
PROVIDER_KINDS = ("fake", "stripe")
PROVIDER_WEBHOOK_SECRET_ENV = "PROVIDER__WEBHOOK_SECRET"  # noqa: S105 - env var name, not a secret
PROVIDER_STRIPE_API_KEY_ENV = "PROVIDER__STRIPE_API_KEY"
DEFAULT_PROVIDER_WEBHOOK_TOLERANCE = timedelta(minutes=5)
DEFAULT_PROVIDER_STRIPE_TIMEOUT = timedelta(seconds=10)
STRIPE_TEST_KEY_PREFIX = "sk_test_"
DEFAULT_PROVIDER_WEBHOOK_PORT = 8085


class ProviderSpec(Section):
    """``spec.provider``: provider selection and webhook intake; secrets come from env."""

    kind: str = ""
    # Deadline одного запроса к Stripe: меньше таймаута Activity orders (30s).
    stripe_timeout: timedelta = DEFAULT_PROVIDER_STRIPE_TIMEOUT
    # HTTP-порт приёма webhooks провайдера, отдельный от gRPC и management.
    webhook_port: int = Field(default=DEFAULT_PROVIDER_WEBHOOK_PORT, ge=1, le=65535)
    # Допустимое расхождение времени подписи webhook и часов payments.
    webhook_tolerance: timedelta = DEFAULT_PROVIDER_WEBHOOK_TOLERANCE

    @field_validator("kind")
    @classmethod
    def _known_kind(cls, value: str) -> str:
        value = value.strip()
        if value and value not in PROVIDER_KINDS:
            raise ValueError(f"{PROVIDER_KIND_KEY} must be one of {PROVIDER_KINDS}, got {value!r}")
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

    @field_validator("webhook_tolerance")
    @classmethod
    def _tolerance_positive(cls, value: timedelta) -> timedelta:
        if value <= timedelta(0):
            raise ValueError("provider webhook tolerance must be positive")
        return value


class ProviderSecrets(BaseSettings):
    """Provider secrets: only from env, in the cluster from a Secret."""

    model_config = SettingsConfigDict(extra="ignore")

    # Общий секрет HMAC подписи webhooks.
    webhook_secret: str = Field(
        default="", validation_alias=PROVIDER_WEBHOOK_SECRET_ENV, repr=False
    )
    # Секретный ключ Stripe test mode; обязателен при spec.provider.kind=stripe.
    stripe_api_key: str = Field(
        default="", validation_alias=PROVIDER_STRIPE_API_KEY_ENV, repr=False
    )

    @field_validator("webhook_secret", "stripe_api_key")
    @classmethod
    def _strip(cls, value: str) -> str:
        return value.strip()


class ProviderSettings(ProviderSpec):
    """Payment provider selection, required by the ``payments`` process.

    ``fake`` is a deterministic in-process sandbox that moves no real money. ``stripe`` is
    the Stripe test mode: only a test key is accepted, so it moves no real money either.
    """

    webhook_secret: str = Field(default="", repr=False)
    stripe_api_key: str = Field(default="", repr=False)

    @classmethod
    def from_spec(cls, spec: ProviderSpec) -> ProviderSettings:
        """``spec.provider`` with the provider secrets of the environment."""
        secrets = ProviderSecrets()
        return cls(
            **spec.model_dump(),
            webhook_secret=secrets.webhook_secret,
            stripe_api_key=secrets.stripe_api_key,
        )

    @model_validator(mode="after")
    def _stripe_test_key(self) -> ProviderSettings:
        if self.kind == "stripe" and not self.stripe_api_key.startswith(STRIPE_TEST_KEY_PREFIX):
            raise ValueError(
                f"{PROVIDER_STRIPE_API_KEY_ENV} must be a test key ({STRIPE_TEST_KEY_PREFIX}...) "
                f"with {PROVIDER_KIND_KEY}=stripe"
            )
        return self

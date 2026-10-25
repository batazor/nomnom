"""Service identity (ADR 27) и user assertions BFF (ADR 32)."""

import re

from pydantic import Field, field_validator, model_validator
from pydantic_settings import BaseSettings, SettingsConfigDict

IDENTITY_ISSUER_ENV = "IDENTITY__ISSUER"
IDENTITY_AUDIENCE_ENV = "IDENTITY__AUDIENCE"
IDENTITY_JWKS_FILE_ENV = "IDENTITY__JWKS_FILE"
IDENTITY_SERVICE_ACCOUNTS_ENV = "IDENTITY__SERVICE_ACCOUNTS"
IDENTITY_USER_ISSUER_ENV = "IDENTITY__USER_ISSUER"
IDENTITY_USER_JWKS_FILE_ENV = "IDENTITY__USER_JWKS_FILE"
IDENTITY_USER_DELEGATES_ENV = "IDENTITY__USER_DELEGATES"

# Сегмент имени Kubernetes (namespace, ServiceAccount): без разделителей ``/``, ``:``, ``=``.
_SERVICE_ACCOUNT_ENTRY = re.compile(r"^([^/:=\s]+)/([^/:=\s]+)=(\S+)$")


def parse_service_accounts(value: str) -> dict[str, str]:
    """Разбирает allowlist ``namespace/serviceaccount=service-name`` через запятую.

    Возвращает отображение ``system:serviceaccount:<ns>:<sa>`` → имя сервиса (ADR 27).
    """
    accounts: dict[str, str] = {}
    if not value.strip():
        return accounts
    for raw in value.split(","):
        entry = raw.strip()
        match = _SERVICE_ACCOUNT_ENTRY.match(entry)
        if match is None:
            raise ValueError(
                f"invalid {IDENTITY_SERVICE_ACCOUNTS_ENV} entry {entry!r}: "
                "expected namespace/serviceaccount=service-name",
            )
        namespace, account, service = match.groups()
        subject = f"system:serviceaccount:{namespace}:{account}"
        if subject in accounts:
            raise ValueError(
                f"duplicate {IDENTITY_SERVICE_ACCOUNTS_ENV} entry for {namespace}/{account}",
            )
        accounts[subject] = service
    return accounts


# Имя сервиса из правой части IDENTITY__SERVICE_ACCOUNTS: без пробелов и запятых.
_USER_DELEGATE_ENTRY = re.compile(r"^[^\s,]+$")


def parse_user_delegates(value: str) -> frozenset[str]:
    """Разбирает allowlist делегатов user assertions — имён сервисов через запятую (ADR 32)."""
    delegates: set[str] = set()
    if not value.strip():
        return frozenset()
    for raw in value.split(","):
        entry = raw.strip()
        if _USER_DELEGATE_ENTRY.match(entry) is None:
            raise ValueError(
                f"invalid {IDENTITY_USER_DELEGATES_ENV} entry {entry!r}: expected service-name",
            )
        if entry in delegates:
            raise ValueError(f"duplicate {IDENTITY_USER_DELEGATES_ENV} entry {entry!r}")
        delegates.add(entry)
    return frozenset(delegates)


class IdentitySettings(BaseSettings):
    """Проверка service identity по projected ServiceAccount token (ADR 27) и user
    assertions BFF (ADR 32).

    Пока issuer, audience или JWKS не заданы, вызовы отклоняются (fail closed). Пока не
    заданы user issuer, user JWKS или делегаты, отклоняется любой вызов с user assertion.
    """

    model_config = SettingsConfigDict(env_file=".env", extra="ignore")

    issuer: str = Field(default="", validation_alias=IDENTITY_ISSUER_ENV)
    audience: str = Field(default="", validation_alias=IDENTITY_AUDIENCE_ENV)
    jwks_file: str = Field(default="", validation_alias=IDENTITY_JWKS_FILE_ENV)
    # ``namespace/serviceaccount=service-name`` через запятую.
    service_accounts: str = Field(default="", validation_alias=IDENTITY_SERVICE_ACCOUNTS_ENV)
    # Ожидаемый ``iss`` и JWKS (ES256) user assertions BFF.
    user_issuer: str = Field(default="", validation_alias=IDENTITY_USER_ISSUER_ENV)
    user_jwks_file: str = Field(default="", validation_alias=IDENTITY_USER_JWKS_FILE_ENV)
    # Имена сервисов, которым разрешено передавать user, через запятую.
    user_delegates: str = Field(default="", validation_alias=IDENTITY_USER_DELEGATES_ENV)

    @field_validator(
        "issuer",
        "audience",
        "jwks_file",
        "service_accounts",
        "user_issuer",
        "user_jwks_file",
        "user_delegates",
    )
    @classmethod
    def _strip(cls, value: str) -> str:
        return value.strip()

    @model_validator(mode="after")
    def _validate_allowlist(self) -> "IdentitySettings":
        accounts = parse_service_accounts(self.service_accounts)
        if self.issuer and not accounts:
            raise ValueError(
                f"{IDENTITY_SERVICE_ACCOUNTS_ENV} must not be empty when "
                f"{IDENTITY_ISSUER_ENV} is set",
            )
        parse_user_delegates(self.user_delegates)
        return self

    @property
    def enabled(self) -> bool:
        return bool(self.issuer and self.audience and self.jwks_file)

    @property
    def service_account_map(self) -> dict[str, str]:
        return parse_service_accounts(self.service_accounts)

    @property
    def user_assertions_enabled(self) -> bool:
        return bool(self.user_issuer and self.user_jwks_file and self.user_delegates)

    @property
    def user_delegate_set(self) -> frozenset[str]:
        return parse_user_delegates(self.user_delegates)

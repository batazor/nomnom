"""Service identity (ADR 27) and BFF user assertions (ADR 32) settings."""

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
    """Parse a comma-separated ``namespace/serviceaccount=service-name`` allowlist.

    Returns:
        Mapping of ``system:serviceaccount:<ns>:<sa>`` to the service name (ADR 27);
        empty for a blank value.

    Raises:
        ValueError: An entry is malformed or a ServiceAccount is listed twice.
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
    """Parse the comma-separated allowlist of services that may delegate a user (ADR 32).

    Raises:
        ValueError: An entry is malformed or listed twice.
    """
    delegates: set[str] = set()
    if not value.strip():
        return frozenset[str]()
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
    """Verification of callers: service identity and BFF user assertions.

    Service identity comes from a projected ServiceAccount token (ADR 27), users from
    BFF user assertions (ADR 32). All of them are required: the resolver refuses to start
    while any is empty (see ``missing``).
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
    def _validate_allowlist(self) -> IdentitySettings:
        accounts = parse_service_accounts(self.service_accounts)
        if self.issuer and not accounts:
            raise ValueError(
                f"{IDENTITY_SERVICE_ACCOUNTS_ENV} must not be empty when "
                f"{IDENTITY_ISSUER_ENV} is set",
            )
        parse_user_delegates(self.user_delegates)
        return self

    @property
    def missing(self) -> list[str]:
        """Environment variables of required identity settings that are empty."""
        required = {
            IDENTITY_ISSUER_ENV: self.issuer,
            IDENTITY_AUDIENCE_ENV: self.audience,
            IDENTITY_JWKS_FILE_ENV: self.jwks_file,
            IDENTITY_SERVICE_ACCOUNTS_ENV: self.service_accounts,
            IDENTITY_USER_ISSUER_ENV: self.user_issuer,
            IDENTITY_USER_JWKS_FILE_ENV: self.user_jwks_file,
            IDENTITY_USER_DELEGATES_ENV: self.user_delegates,
        }
        return [env for env, value in required.items() if not value]

    @property
    def service_account_map(self) -> dict[str, str]:
        """Parsed ServiceAccount allowlist; see ``parse_service_accounts``."""
        return parse_service_accounts(self.service_accounts)

    @property
    def user_delegate_set(self) -> frozenset[str]:
        """Parsed allowlist of services that may pass a user on its behalf."""
        return parse_user_delegates(self.user_delegates)

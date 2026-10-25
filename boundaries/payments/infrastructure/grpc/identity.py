"""Caller identity for gRPC: verified projected ServiceAccount tokens (ADR 27).

The verification contract is shared by every context and pinned by the vectors in
``tests/identity``; see ``openspec/changes/add-service-identity/design.md``.
"""

import json
import logging
import time
from collections.abc import Callable, Mapping
from pathlib import Path
from typing import NoReturn, Protocol

import grpc
import jwt
from jwt import PyJWK, PyJWKSet

from pkg.application import Subject, SubjectKind
from pkg.config import IdentitySettings

logger = logging.getLogger(__name__)

ALLOWED_ALGORITHMS = frozenset({"RS256", "ES256"})
LEEWAY_SECONDS = 30
_REQUIRED_CLAIMS = ["exp", "iss", "aud", "sub"]
_REJECTED = "invalid caller identity"


class SubjectResolver(Protocol):
    def resolve(self, context: grpc.ServicerContext) -> Subject: ...


class RejectingSubjectResolver:
    """Deny by default until verified identity is configured."""

    def resolve(self, context: grpc.ServicerContext) -> NoReturn:
        context.abort(grpc.StatusCode.UNAUTHENTICATED, "verified caller identity is required")


class IdentityConfigError(ValueError):
    """Identity configuration that must stop the process at startup."""


class _TokenRejected(Exception):
    """Specific rejection reason; logged, never returned to the caller."""


def load_jwks(path: str | Path) -> dict[str, PyJWK]:
    """Load signing keys by ``kid``; an unreadable or unusable JWKS is a startup error."""
    try:
        document = json.loads(Path(path).read_text(encoding="utf-8"))
    except (OSError, ValueError) as error:
        raise IdentityConfigError(f"cannot read JWKS file {path}: {error}") from error
    if not isinstance(document, dict) or not isinstance(document.get("keys"), list):
        raise IdentityConfigError(f"invalid JWKS file {path}: expected an object with keys")
    try:
        key_set = PyJWKSet.from_dict(document)
    except jwt.PyJWTError as error:
        raise IdentityConfigError(f"invalid JWKS file {path}: {error}") from error

    keys: dict[str, PyJWK] = {}
    for key in key_set.keys:
        if not key.key_id or key.algorithm_name not in ALLOWED_ALGORITHMS:
            continue
        if key.public_key_use not in (None, "sig"):
            continue
        if key.key_id in keys:
            raise IdentityConfigError(f"invalid JWKS file {path}: duplicate kid {key.key_id!r}")
        keys[key.key_id] = key
    if not keys:
        raise IdentityConfigError(
            f"invalid JWKS file {path}: no RS256/ES256 signing keys with kid",
        )
    return keys


class ServiceTokenSubjectResolver:
    """Resolve ``Subject(service, name)`` from ``authorization: Bearer <jwt>`` metadata."""

    def __init__(
        self,
        *,
        issuer: str,
        audience: str,
        keys: Mapping[str, PyJWK],
        service_accounts: Mapping[str, str],
        clock: Callable[[], float] = time.time,
    ) -> None:
        if not issuer or not audience:
            raise IdentityConfigError("identity issuer and audience must not be empty")
        if not keys:
            raise IdentityConfigError("identity JWKS must contain signing keys")
        if not service_accounts:
            raise IdentityConfigError("identity service account allowlist must not be empty")
        self._issuer = issuer
        self._audience = audience
        self._keys = dict(keys)
        self._service_accounts = dict(service_accounts)
        self._clock = clock

    def resolve(self, context: grpc.ServicerContext) -> Subject:
        try:
            return self._verify(_bearer_token(context))
        except _TokenRejected as error:
            reason = str(error)
        except jwt.PyJWTError as error:
            reason = f"{type(error).__name__}: {error}"
        logger.warning("rejected caller identity: %s", reason)
        context.abort(grpc.StatusCode.UNAUTHENTICATED, _REJECTED)
        raise AssertionError("context.abort must raise")  # pragma: no cover

    def _verify(self, token: str) -> Subject:
        header = jwt.get_unverified_header(token)
        algorithm = header.get("alg")
        if algorithm not in ALLOWED_ALGORITHMS:
            raise _TokenRejected(f"algorithm {algorithm!r} is not allowed")
        kid = header.get("kid")
        if not isinstance(kid, str) or not kid:
            raise _TokenRejected("token has no kid")
        key = self._keys.get(kid)
        if key is None:
            raise _TokenRejected(f"unknown kid {kid!r}")
        if key.algorithm_name != algorithm:
            raise _TokenRejected(f"algorithm {algorithm!r} does not match key {kid!r}")

        # exp/nbf are checked below against the injectable clock.
        claims = jwt.decode(
            token,
            key=key.key,
            algorithms=[algorithm],
            audience=self._audience,
            issuer=self._issuer,
            options={
                "require": _REQUIRED_CLAIMS,
                "verify_exp": False,
                "verify_nbf": False,
                "verify_iat": False,
            },
        )
        now = self._clock()
        expires_at = _numeric_claim(claims, "exp")
        if expires_at <= now - LEEWAY_SECONDS:
            raise _TokenRejected("token is expired")
        if "nbf" in claims and _numeric_claim(claims, "nbf") > now + LEEWAY_SECONDS:
            raise _TokenRejected("token is not yet valid")

        subject = claims.get("sub")
        service = self._service_accounts.get(subject) if isinstance(subject, str) else None
        if service is None:
            raise _TokenRejected(f"service account {subject!r} is not allowed")
        return Subject(SubjectKind.SERVICE, service)


def _bearer_token(context: grpc.ServicerContext) -> str:
    values = [
        value
        for key, value in (context.invocation_metadata() or ())
        if key.lower() == "authorization"
    ]
    if not values:
        raise _TokenRejected("authorization metadata is missing")
    if len(values) > 1:
        raise _TokenRejected("multiple authorization metadata values")
    value = values[0]
    if isinstance(value, bytes):
        raise _TokenRejected("authorization metadata is binary")
    scheme, _, token = value.partition(" ")
    if scheme.lower() != "bearer" or not token.strip():
        raise _TokenRejected("authorization scheme is not Bearer")
    return token.strip()


def _numeric_claim(claims: Mapping[str, object], name: str) -> float:
    value = claims.get(name)
    if isinstance(value, bool) or not isinstance(value, (int, float)):
        raise _TokenRejected(f"claim {name} is not a number")
    return float(value)


def create_subject_resolver(
    settings: IdentitySettings,
    clock: Callable[[], float] = time.time,
) -> SubjectResolver:
    """Build the production resolver; without issuer, audience or JWKS fail closed."""
    if not settings.enabled:
        logger.warning(
            "service identity is not configured (IDENTITY__ISSUER, IDENTITY__AUDIENCE, "
            "IDENTITY__JWKS_FILE): all calls are rejected",
        )
        return RejectingSubjectResolver()
    return ServiceTokenSubjectResolver(
        issuer=settings.issuer,
        audience=settings.audience,
        keys=load_jwks(settings.jwks_file),
        service_accounts=settings.service_account_map,
        clock=clock,
    )

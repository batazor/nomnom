"""Caller identity for gRPC: verified projected ServiceAccount tokens (ADR 27) and
user assertions signed by BFF (ADR 32).

The verification contract is shared by every context and pinned by the vectors in
``tests/identity``; see ``openspec/changes/add-service-identity/design.md`` and
``openspec/changes/add-user-identity-assertions/design.md``.
"""

import json
import logging
import time
from collections.abc import Callable, Collection, Mapping
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

USER_ASSERTION_METADATA = "x-nomnom-user-assertion"
USER_ASSERTION_ALGORITHM = "ES256"
USER_ASSERTION_MAX_TTL_SECONDS = 60
_USER_REQUIRED_CLAIMS = ["exp", "iat", "iss", "aud", "sub", "jti"]


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


def load_jwks(
    path: str | Path,
    algorithms: Collection[str] = ALLOWED_ALGORITHMS,
) -> dict[str, PyJWK]:
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
        if not key.key_id or key.algorithm_name not in algorithms:
            continue
        if key.public_key_use not in (None, "sig"):
            continue
        if key.key_id in keys:
            raise IdentityConfigError(f"invalid JWKS file {path}: duplicate kid {key.key_id!r}")
        keys[key.key_id] = key
    if not keys:
        names = "/".join(sorted(algorithms))
        raise IdentityConfigError(f"invalid JWKS file {path}: no {names} signing keys with kid")
    return keys


def load_user_jwks(path: str | Path) -> dict[str, PyJWK]:
    """Load the ES256 (EC P-256) keys of BFF user assertions; anything else is skipped."""
    keys = load_jwks(path, algorithms={USER_ASSERTION_ALGORITHM})
    for kid, key in keys.items():
        curve = getattr(key.key, "curve", None)
        if getattr(curve, "name", None) != "secp256r1":
            raise IdentityConfigError(f"invalid JWKS file {path}: key {kid!r} is not EC P-256")
    return keys


class UserAssertionVerifier:
    """Verify ``x-nomnom-user-assertion``: ES256 JWT of BFF for this context (ADR 32)."""

    def __init__(
        self,
        *,
        issuer: str,
        audience: str,
        keys: Mapping[str, PyJWK],
        clock: Callable[[], float] = time.time,
    ) -> None:
        if not issuer or not audience:
            raise IdentityConfigError("user assertion issuer and audience must not be empty")
        if not keys:
            raise IdentityConfigError("user assertion JWKS must contain signing keys")
        self._issuer = issuer
        self._audience = audience
        self._keys = dict(keys)
        self._clock = clock

    def verify(self, token: str) -> tuple[str, str]:
        """Return ``(sub, jti)`` of a valid assertion; raise on any violation."""
        header = jwt.get_unverified_header(token)
        algorithm = header.get("alg")
        if algorithm != USER_ASSERTION_ALGORITHM:
            raise _TokenRejected(f"assertion algorithm {algorithm!r} is not allowed")
        kid = header.get("kid")
        if not isinstance(kid, str) or not kid:
            raise _TokenRejected("assertion has no kid")
        key = self._keys.get(kid)
        if key is None:
            raise _TokenRejected(f"unknown assertion kid {kid!r}")

        # exp/nbf are checked below against the injectable clock.
        claims = jwt.decode(
            token,
            key=key.key,
            algorithms=[USER_ASSERTION_ALGORITHM],
            audience=self._audience,
            issuer=self._issuer,
            options={
                "require": _USER_REQUIRED_CLAIMS,
                "verify_exp": False,
                "verify_nbf": False,
                "verify_iat": False,
            },
        )
        now = self._clock()
        expires_at = _numeric_claim(claims, "exp")
        if expires_at <= now - LEEWAY_SECONDS:
            raise _TokenRejected("assertion is expired")
        if "nbf" in claims and _numeric_claim(claims, "nbf") > now + LEEWAY_SECONDS:
            raise _TokenRejected("assertion is not yet valid")
        if expires_at - _numeric_claim(claims, "iat") > USER_ASSERTION_MAX_TTL_SECONDS:
            raise _TokenRejected("assertion lifetime exceeds 60 s")

        subject = claims.get("sub")
        if not isinstance(subject, str) or not subject or subject != subject.strip():
            raise _TokenRejected("assertion sub is empty or padded")
        jti = claims.get("jti")
        if not isinstance(jti, str) or not jti.strip():
            raise _TokenRejected("assertion jti is empty")
        return subject, jti


class ServiceTokenSubjectResolver:
    """Resolve ``Subject(service, name)`` from ``authorization: Bearer <jwt>`` metadata.

    With ``x-nomnom-user-assertion`` the authenticated service must be a user delegate and
    the assertion must verify; the result is ``Subject(user, sub)`` with the delegate kept.
    Without ``user_assertions`` every call carrying an assertion is rejected.
    """

    def __init__(
        self,
        *,
        issuer: str,
        audience: str,
        keys: Mapping[str, PyJWK],
        service_accounts: Mapping[str, str],
        clock: Callable[[], float] = time.time,
        user_assertions: UserAssertionVerifier | None = None,
        user_delegates: Collection[str] = (),
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
        self._user_assertions = user_assertions
        self._user_delegates = frozenset(user_delegates)

    def resolve(self, context: grpc.ServicerContext) -> Subject:
        try:
            service = self._verify(_bearer_token(context))
        except _TokenRejected as error:
            reason = str(error)
        except jwt.PyJWTError as error:
            reason = f"{type(error).__name__}: {error}"
        else:
            return self._resolve_user(context, service)
        logger.warning("rejected caller identity: %s", reason)
        self._abort(context)

    def _resolve_user(self, context: grpc.ServicerContext, service: Subject) -> Subject:
        values = [
            value
            for key, value in (context.invocation_metadata() or ())
            if key.lower() == USER_ASSERTION_METADATA
        ]
        if not values:
            return service
        jti: object = None
        try:
            if len(values) > 1:
                raise _TokenRejected(f"multiple {USER_ASSERTION_METADATA} metadata values")
            token = values[0]
            if isinstance(token, bytes) or not token.strip():
                raise _TokenRejected(f"{USER_ASSERTION_METADATA} metadata is empty or binary")
            jti = _unverified_jti(token)
            if self._user_assertions is None:
                raise _TokenRejected("user assertions are not configured")
            if service.id not in self._user_delegates:
                raise _TokenRejected(f"service {service.id!r} is not a user delegate")
            user, jti = self._user_assertions.verify(token.strip())
        except _TokenRejected as error:
            reason = str(error)
        except jwt.PyJWTError as error:
            reason = f"{type(error).__name__}: {error}"
        else:
            logger.info("accepted user assertion: delegate=%s jti=%s", service.id, jti)
            return Subject(SubjectKind.USER, user, delegate=service.id)
        logger.warning("rejected user assertion: delegate=%s jti=%r: %s", service.id, jti, reason)
        self._abort(context)

    @staticmethod
    def _abort(context: grpc.ServicerContext) -> NoReturn:
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


def _unverified_jti(token: str) -> object:
    """``jti`` for correlating a rejection in logs; the claim is not trusted."""
    try:
        claims = jwt.decode(token.strip(), options={"verify_signature": False})
    except jwt.PyJWTError:
        return None
    jti = claims.get("jti")
    return jti[:128] if isinstance(jti, str) else None


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
        user_assertions=create_user_assertion_verifier(settings, clock),
        user_delegates=settings.user_delegate_set,
    )


def create_user_assertion_verifier(
    settings: IdentitySettings,
    clock: Callable[[], float] = time.time,
) -> UserAssertionVerifier | None:
    """Build the BFF assertion verifier; without user issuer, JWKS or delegates, none.

    A configured but unusable user JWKS is a startup error even when the rest is missing.
    """
    keys = load_user_jwks(settings.user_jwks_file) if settings.user_jwks_file else None
    if keys is None or not settings.user_assertions_enabled:
        logger.info(
            "user assertions are not configured (IDENTITY__USER_ISSUER, "
            "IDENTITY__USER_JWKS_FILE, IDENTITY__USER_DELEGATES): calls carrying "
            "x-nomnom-user-assertion are rejected",
        )
        return None
    return UserAssertionVerifier(
        issuer=settings.user_issuer,
        audience=settings.audience,
        keys=keys,
        clock=clock,
    )

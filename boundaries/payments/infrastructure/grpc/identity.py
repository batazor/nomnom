"""Caller identity for gRPC: verified service tokens and BFF user assertions.

Service identity comes from projected ServiceAccount tokens (ADR 27), user identity from
assertions signed by BFF (ADR 32).

The verification contract is shared by every context and pinned by the vectors in
``tests/identity``; see ``openspec/changes/add-service-identity/design.md`` and
``openspec/changes/add-user-identity-assertions/design.md``.
"""

import logging
import time
from collections.abc import Callable, Collection, Mapping
from typing import NoReturn, Protocol

import grpc
import jwt
from jwt import PyJWK

from pkg.application import Subject, SubjectKind
from pkg.config import IdentitySettings

from .identity_tokens import (
    _REJECTED,
    _REQUIRED_CLAIMS,
    ALLOWED_ALGORITHMS,
    LEEWAY_SECONDS,
    USER_ASSERTION_ALGORITHM,
    USER_ASSERTION_MAX_TTL_SECONDS,
    USER_ASSERTION_METADATA,
    IdentityConfigError,
    _bearer_token,
    _numeric_claim,
    _TokenRejectedError,
    _unverified_jti,
    load_jwks,
    load_user_jwks,
)
from .user_assertion import UserAssertionVerifier

__all__ = [
    "ALLOWED_ALGORITHMS",
    "LEEWAY_SECONDS",
    "USER_ASSERTION_ALGORITHM",
    "USER_ASSERTION_MAX_TTL_SECONDS",
    "USER_ASSERTION_METADATA",
    "IdentityConfigError",
    "RejectingSubjectResolver",
    "ServiceTokenSubjectResolver",
    "SubjectResolver",
    "UserAssertionVerifier",
    "create_subject_resolver",
    "create_user_assertion_verifier",
    "load_jwks",
    "load_user_jwks",
]

logger = logging.getLogger(__name__)


class SubjectResolver(Protocol):
    """Turns the metadata of an incoming gRPC call into the verified caller ``Subject``."""

    def resolve(self, context: grpc.ServicerContext) -> Subject:
        """Return the caller, or abort the call with ``UNAUTHENTICATED``."""
        ...


class RejectingSubjectResolver:
    """Deny by default until verified identity is configured."""

    def resolve(self, context: grpc.ServicerContext) -> NoReturn:
        """Abort every call with ``UNAUTHENTICATED``."""
        context.abort(grpc.StatusCode.UNAUTHENTICATED, "verified caller identity is required")


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

    def resolve(self, context: grpc.ServicerContext) -> Subject:  # noqa: RET503 - _abort raises
        """Verify the caller and return its ``Subject``.

        Any rejected token or assertion aborts the call with ``UNAUTHENTICATED`` and a generic
        message; the reason is logged only.
        """
        try:
            service = self._verify(_bearer_token(context))
        except _TokenRejectedError as error:
            reason = str(error)
        except jwt.PyJWTError as error:
            reason = f"{type(error).__name__}: {error}"
        else:
            return self._resolve_user(context, service)
        logger.warning("rejected caller identity: %s", reason)
        self._abort(context)

    def _resolve_user(self, context: grpc.ServicerContext, service: Subject) -> Subject:  # noqa: RET503 - _abort raises
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
                raise _TokenRejectedError(f"multiple {USER_ASSERTION_METADATA} metadata values")
            token = values[0]
            if isinstance(token, bytes) or not token.strip():
                raise _TokenRejectedError(f"{USER_ASSERTION_METADATA} metadata is empty or binary")
            jti = _unverified_jti(token)
            if self._user_assertions is None:
                raise _TokenRejectedError("user assertions are not configured")
            if service.id not in self._user_delegates:
                raise _TokenRejectedError(f"service {service.id!r} is not a user delegate")
            user, jti = self._user_assertions.verify(token.strip())
        except _TokenRejectedError as error:
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
            raise _TokenRejectedError(f"algorithm {algorithm!r} is not allowed")
        kid = header.get("kid")
        if not isinstance(kid, str) or not kid:
            raise _TokenRejectedError("token has no kid")
        key = self._keys.get(kid)
        if key is None:
            raise _TokenRejectedError(f"unknown kid {kid!r}")
        if key.algorithm_name != algorithm:
            raise _TokenRejectedError(f"algorithm {algorithm!r} does not match key {kid!r}")

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
            raise _TokenRejectedError("token is expired")
        if "nbf" in claims and _numeric_claim(claims, "nbf") > now + LEEWAY_SECONDS:
            raise _TokenRejectedError("token is not yet valid")

        subject = claims.get("sub")
        service = self._service_accounts.get(subject) if isinstance(subject, str) else None
        if service is None:
            raise _TokenRejectedError(f"service account {subject!r} is not allowed")
        return Subject(SubjectKind.SERVICE, service)


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

"""Caller identity for gRPC: verified service tokens and BFF user assertions.

Service identity comes from projected ServiceAccount tokens (ADR 27), user identity from
assertions signed by BFF (ADR 32).

The verification contract is shared by every context and pinned by the vectors in
``tests/identity``; see ``openspec/changes/add-service-identity/design.md`` and
``openspec/changes/add-user-identity-assertions/design.md``.
"""

import logging
import time
from collections.abc import Callable, Collection
from typing import NoReturn, Protocol

import grpc
import jwt

from pkg.application import Subject, SubjectKind
from pkg.config import IdentitySettings

from .identity_tokens import (
    ALLOWED_ALGORITHMS,
    LEEWAY_SECONDS,
    USER_ASSERTION_ALGORITHM,
    USER_ASSERTION_MAX_TTL_SECONDS,
    USER_ASSERTION_METADATA,
    IdentityConfigError,
    TokenRejectedError,
    load_jwks,
    load_user_jwks,
    unverified_jti,
)
from .service_token import ServiceTokenVerifier
from .user_assertion import UserAssertionVerifier

__all__ = [
    "ALLOWED_ALGORITHMS",
    "LEEWAY_SECONDS",
    "USER_ASSERTION_ALGORITHM",
    "USER_ASSERTION_MAX_TTL_SECONDS",
    "USER_ASSERTION_METADATA",
    "IdentityConfigError",
    "ServiceTokenSubjectResolver",
    "ServiceTokenVerifier",
    "SubjectResolver",
    "UserAssertionVerifier",
    "create_subject_resolver",
    "load_jwks",
    "load_user_jwks",
]

logger = logging.getLogger(__name__)

_REJECTED = "invalid caller identity"


class SubjectResolver(Protocol):
    """Turns the metadata of an incoming gRPC call into the verified caller ``Subject``."""

    def resolve(self, context: grpc.ServicerContext) -> Subject:
        """Return the caller, or abort the call with ``UNAUTHENTICATED``."""
        ...


class ServiceTokenSubjectResolver:
    """Resolve the caller ``Subject`` from gRPC metadata.

    Reads ``authorization: Bearer <jwt>`` and ``x-nomnom-user-assertion`` and composes the
    transport-free verifiers: the service token yields ``Subject(service, name)``; with an
    assertion the authenticated service must be a user delegate and the assertion must
    verify, and the result is ``Subject(user, sub)`` with the delegate kept.
    """

    def __init__(
        self,
        service_tokens: ServiceTokenVerifier,
        *,
        user_assertions: UserAssertionVerifier,
        user_delegates: Collection[str],
    ) -> None:
        self._service_tokens = service_tokens
        self._user_assertions = user_assertions
        self._user_delegates = frozenset(user_delegates)

    def resolve(self, context: grpc.ServicerContext) -> Subject:  # noqa: RET503 - _abort raises
        """Verify the caller and return its ``Subject``.

        Any rejected token or assertion aborts the call with ``UNAUTHENTICATED`` and a generic
        message; the reason is logged only.
        """
        try:
            service = self._service_tokens.verify(_bearer_token(context))
        except TokenRejectedError as error:
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
                raise TokenRejectedError(f"multiple {USER_ASSERTION_METADATA} metadata values")
            token = values[0]
            if isinstance(token, bytes) or not token.strip():
                raise TokenRejectedError(f"{USER_ASSERTION_METADATA} metadata is empty or binary")
            jti = unverified_jti(token)
            if service.id not in self._user_delegates:
                raise TokenRejectedError(f"service {service.id!r} is not a user delegate")
            user, jti = self._user_assertions.verify(token.strip())
        except TokenRejectedError as error:
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


def _bearer_token(context: grpc.ServicerContext) -> str:
    values = [
        value
        for key, value in (context.invocation_metadata() or ())
        if key.lower() == "authorization"
    ]
    if not values:
        raise TokenRejectedError("authorization metadata is missing")
    if len(values) > 1:
        raise TokenRejectedError("multiple authorization metadata values")
    value = values[0]
    if isinstance(value, bytes):
        raise TokenRejectedError("authorization metadata is binary")
    scheme, _, token = value.partition(" ")
    if scheme.lower() != "bearer" or not token.strip():
        raise TokenRejectedError("authorization scheme is not Bearer")
    return token.strip()


def create_subject_resolver(
    settings: IdentitySettings,
    clock: Callable[[], float] = time.time,
) -> SubjectResolver:
    """Build the production resolver; service identity and BFF user assertions are required.

    An empty setting, an unusable JWKS or a malformed allowlist is a startup error.
    """
    missing = settings.missing
    if missing:
        raise IdentityConfigError(f"{', '.join(missing)} must be set")
    return ServiceTokenSubjectResolver(
        ServiceTokenVerifier(
            issuer=settings.issuer,
            audience=settings.audience,
            keys=load_jwks(settings.jwks_file),
            service_accounts=settings.service_account_map,
            clock=clock,
        ),
        user_assertions=UserAssertionVerifier(
            issuer=settings.user_issuer,
            audience=settings.audience,
            keys=load_user_jwks(settings.user_jwks_file),
            clock=clock,
        ),
        user_delegates=settings.user_delegate_set,
    )

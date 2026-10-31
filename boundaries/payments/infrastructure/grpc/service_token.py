"""Verification of projected ServiceAccount tokens (ADR 27)."""

import time
from collections.abc import Callable, Mapping

import jwt
from jwt import PyJWK

from pkg.authz import Subject, SubjectKind

from .identity_tokens import (
    ALLOWED_ALGORITHMS,
    LEEWAY_SECONDS,
    SERVICE_TOKEN_REQUIRED_CLAIMS,
    IdentityConfigError,
    TokenRejectedError,
    numeric_claim,
)


class ServiceTokenVerifier:
    """Verify a service token: RS256/ES256 JWT of an allowed ServiceAccount for this context.

    Transport-free: the resolver reads ``authorization: Bearer <jwt>`` and passes the token.
    """

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

    def verify(self, token: str) -> Subject:
        """Return ``Subject(service, name)`` of the allowed ServiceAccount.

        Raises:
            TokenRejectedError: A specific contract violation.
            jwt.PyJWTError: The token is malformed or its signature or claims are invalid.
        """
        header = jwt.get_unverified_header(token)
        algorithm = header.get("alg")
        if algorithm not in ALLOWED_ALGORITHMS:
            raise TokenRejectedError(f"algorithm {algorithm!r} is not allowed")
        kid = header.get("kid")
        if not isinstance(kid, str) or not kid:
            raise TokenRejectedError("token has no kid")
        key = self._keys.get(kid)
        if key is None:
            raise TokenRejectedError(f"unknown kid {kid!r}")
        if key.algorithm_name != algorithm:
            raise TokenRejectedError(f"algorithm {algorithm!r} does not match key {kid!r}")

        # exp/nbf are checked below against the injectable clock.
        claims = jwt.decode(
            token,
            key=key.key,
            algorithms=[algorithm],
            audience=self._audience,
            issuer=self._issuer,
            options={
                "require": SERVICE_TOKEN_REQUIRED_CLAIMS,
                "verify_exp": False,
                "verify_nbf": False,
                "verify_iat": False,
            },
        )
        now = self._clock()
        expires_at = numeric_claim(claims, "exp")
        if expires_at <= now - LEEWAY_SECONDS:
            raise TokenRejectedError("token is expired")
        if "nbf" in claims and numeric_claim(claims, "nbf") > now + LEEWAY_SECONDS:
            raise TokenRejectedError("token is not yet valid")

        subject = claims.get("sub")
        service = self._service_accounts.get(subject) if isinstance(subject, str) else None
        if service is None:
            raise TokenRejectedError(f"service account {subject!r} is not allowed")
        return Subject(SubjectKind.SERVICE, service)

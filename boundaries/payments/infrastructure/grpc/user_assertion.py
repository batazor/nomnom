"""Verification of user assertions signed by BFF (ADR 32)."""

import time
from collections.abc import Callable, Mapping

import jwt
from jwt import PyJWK

from .identity_tokens import (
    _USER_REQUIRED_CLAIMS,
    LEEWAY_SECONDS,
    USER_ASSERTION_ALGORITHM,
    USER_ASSERTION_MAX_TTL_SECONDS,
    IdentityConfigError,
    _numeric_claim,
    _TokenRejectedError,
)


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
            raise _TokenRejectedError(f"assertion algorithm {algorithm!r} is not allowed")
        kid = header.get("kid")
        if not isinstance(kid, str) or not kid:
            raise _TokenRejectedError("assertion has no kid")
        key = self._keys.get(kid)
        if key is None:
            raise _TokenRejectedError(f"unknown assertion kid {kid!r}")

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
            raise _TokenRejectedError("assertion is expired")
        if "nbf" in claims and _numeric_claim(claims, "nbf") > now + LEEWAY_SECONDS:
            raise _TokenRejectedError("assertion is not yet valid")
        if expires_at - _numeric_claim(claims, "iat") > USER_ASSERTION_MAX_TTL_SECONDS:
            raise _TokenRejectedError("assertion lifetime exceeds 60 s")

        subject = claims.get("sub")
        if not isinstance(subject, str) or not subject or subject != subject.strip():
            raise _TokenRejectedError("assertion sub is empty or padded")
        jti = claims.get("jti")
        if not isinstance(jti, str) or not jti.strip():
            raise _TokenRejectedError("assertion jti is empty")
        return subject, jti

"""Token building blocks of caller identity: constants, JWKS loading and claim checks
shared by service tokens (ADR 27) and BFF user assertions (ADR 32)."""

import json
from collections.abc import Collection, Mapping
from pathlib import Path

import grpc
import jwt
from jwt import PyJWK, PyJWKSet

ALLOWED_ALGORITHMS = frozenset({"RS256", "ES256"})
LEEWAY_SECONDS = 30
_REQUIRED_CLAIMS = ["exp", "iss", "aud", "sub"]
_REJECTED = "invalid caller identity"

USER_ASSERTION_METADATA = "x-nomnom-user-assertion"
USER_ASSERTION_ALGORITHM = "ES256"
USER_ASSERTION_MAX_TTL_SECONDS = 60
_USER_REQUIRED_CLAIMS = ["exp", "iat", "iss", "aud", "sub", "jti"]


class IdentityConfigError(ValueError):
    """Identity configuration that must stop the process at startup."""


class _TokenRejectedError(Exception):
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


def _bearer_token(context: grpc.ServicerContext) -> str:
    values = [
        value
        for key, value in (context.invocation_metadata() or ())
        if key.lower() == "authorization"
    ]
    if not values:
        raise _TokenRejectedError("authorization metadata is missing")
    if len(values) > 1:
        raise _TokenRejectedError("multiple authorization metadata values")
    value = values[0]
    if isinstance(value, bytes):
        raise _TokenRejectedError("authorization metadata is binary")
    scheme, _, token = value.partition(" ")
    if scheme.lower() != "bearer" or not token.strip():
        raise _TokenRejectedError("authorization scheme is not Bearer")
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
        raise _TokenRejectedError(f"claim {name} is not a number")
    return float(value)

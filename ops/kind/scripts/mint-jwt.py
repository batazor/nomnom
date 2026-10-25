#!/usr/bin/env python3
"""Short-lived test JWTs for the kind checks of ADR 32 (stdlib and openssl only).

access-token <idp-key.pem> <idp-jwks.json> <idp-vectors.json> <sub>
    RS256 access token of the test identity provider (tests/identity/fixtures):
    iss and aud from idp-vectors.json, typ at+jwt, scope, 120 s lifetime. Only for kind.
user-assertion <bff-key.pem> <kid> <audience> <sub>
    ES256 user assertion shaped like the ones BFF signs: iss nomnom-bff, 30 s lifetime, jti.
"""
import base64
import json
import subprocess
import sys
import time
import uuid


def b64(data: bytes) -> str:
    return base64.urlsafe_b64encode(data).rstrip(b"=").decode()


def signing_input(header: dict, claims: dict) -> str:
    enc = lambda value: b64(json.dumps(value, separators=(",", ":")).encode())
    return f"{enc(header)}.{enc(claims)}"


def openssl_sign(data: str, key_file: str) -> bytes:
    return subprocess.run(
        ["openssl", "dgst", "-sha256", "-sign", key_file], input=data.encode(), capture_output=True, check=True
    ).stdout


def der_to_raw(der: bytes) -> bytes:
    # ECDSA-Sig-Value ::= SEQUENCE { r INTEGER, s INTEGER } -> r || s, 32 bytes each.
    assert der[0] == 0x30
    i = 2 if der[1] < 0x80 else 2 + (der[1] & 0x7F)
    parts = []
    for _ in range(2):
        assert der[i] == 0x02
        length = der[i + 1]
        parts.append(der[i + 2 : i + 2 + length].lstrip(b"\x00").rjust(32, b"\x00"))
        i += 2 + length
    return b"".join(parts)


def access_token(key_file: str, jwks_file: str, vectors_file: str, sub: str) -> str:
    vectors = json.load(open(vectors_file))
    kid = json.load(open(jwks_file))["keys"][0]["kid"]
    now = int(time.time())
    claims = {
        "iss": vectors["issuer"],
        "aud": vectors["audience"],
        "sub": sub,
        "scope": "openid orders",
        "iat": now,
        "nbf": now,
        "exp": now + 120,
    }
    data = signing_input({"alg": "RS256", "kid": kid, "typ": "at+jwt"}, claims)
    return f"{data}.{b64(openssl_sign(data, key_file))}"


def user_assertion(key_file: str, kid: str, audience: str, sub: str) -> str:
    now = int(time.time())
    claims = {
        "iss": "nomnom-bff",
        "aud": [audience],
        "sub": sub,
        "jti": str(uuid.uuid4()),
        "iat": now,
        "nbf": now,
        "exp": now + 30,
    }
    data = signing_input({"alg": "ES256", "kid": kid, "typ": "JWT"}, claims)
    return f"{data}.{b64(der_to_raw(openssl_sign(data, key_file)))}"


if __name__ == "__main__":
    command, *args = sys.argv[1:]
    if command == "access-token" and len(args) == 4:
        print(access_token(*args))
    elif command == "user-assertion" and len(args) == 4:
        print(user_assertion(*args))
    else:
        sys.exit(__doc__)

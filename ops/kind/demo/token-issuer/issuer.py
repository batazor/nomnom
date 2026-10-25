#!/usr/bin/env python3
"""Dev token issuer of the kind environment (stdlib only). Never deployed outside kind.

GET /tokens returns short-lived access tokens of the test identity provider
(tests/identity/fixtures) for the fixed demo subjects, signed like
`ops/kind/scripts/mint-jwt.py access-token`, plus the demo restaurants written by
ops/kind/scripts/seed-demo.py:

    {"expiresAt": <unix seconds>, "tokens": {"customer": "...", ...},
     "subjects": {"customer": "nomnom-demo-customer", ...},
     "restaurants": [{"id": "...", "name": "..."}]}

Configuration (environment):
    KEY_FILE, JWKS_FILE, VECTORS_FILE — test IdP key (PKCS#8 PEM), JWKS and vectors;
    RESTAURANTS_FILE — JSON list of restaurants (optional until the seed has run);
    ALLOWED_ORIGINS — comma-separated CORS origins; PORT; TOKEN_TTL_SECONDS.
"""
import base64
import hashlib
import json
import os
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

SUBJECTS = {
    "customer": "nomnom-demo-customer",
    "owner": "nomnom-demo-owner",
    "kitchen": "nomnom-demo-kitchen",
    "courier": "nomnom-demo-courier",
}
# DigestInfo prefix of SHA-256 for RSASSA-PKCS1-v1_5 (RFC 8017, section 9.2).
SHA256_DIGEST_INFO = bytes.fromhex("3031300d060960864801650304020105000420")


def der_read(data: bytes, offset: int) -> tuple[int, bytes, int]:
    """Reads one DER TLV at offset: (tag, value, next offset)."""
    tag, length = data[offset], data[offset + 1]
    offset += 2
    if length & 0x80:
        size = length & 0x7F
        length = int.from_bytes(data[offset : offset + size], "big")
        offset += size
    return tag, data[offset : offset + length], offset + length


def rsa_key(pem: str) -> tuple[int, int]:
    """Modulus and private exponent of a PKCS#8 RSA private key."""
    body = "".join(line for line in pem.splitlines() if not line.startswith("-----"))
    _, info, _ = der_read(base64.b64decode(body), 0)
    _, _, offset = der_read(info, 0)  # version
    _, _, offset = der_read(info, offset)  # algorithm
    _, rsa, _ = der_read(info, offset)  # OCTET STRING with RSAPrivateKey
    _, seq, _ = der_read(rsa, 0)
    _, _, offset = der_read(seq, 0)  # version
    _, n, offset = der_read(seq, offset)
    _, _, offset = der_read(seq, offset)  # public exponent
    _, d, _ = der_read(seq, offset)
    return int.from_bytes(n, "big"), int.from_bytes(d, "big")


def b64(data: bytes) -> str:
    return base64.urlsafe_b64encode(data).rstrip(b"=").decode()


class Issuer:
    def __init__(self) -> None:
        self.n, self.d = rsa_key(open(os.environ["KEY_FILE"]).read())
        self.size = (self.n.bit_length() + 7) // 8
        self.kid = json.load(open(os.environ["JWKS_FILE"]))["keys"][0]["kid"]
        vectors = json.load(open(os.environ["VECTORS_FILE"]))
        self.issuer, self.audience = vectors["issuer"], vectors["audience"]
        self.ttl = int(os.environ.get("TOKEN_TTL_SECONDS", "3600"))

    def sign(self, data: bytes) -> bytes:
        digest = SHA256_DIGEST_INFO + hashlib.sha256(data).digest()
        padded = b"\x00\x01" + b"\xff" * (self.size - len(digest) - 3) + b"\x00" + digest
        return pow(int.from_bytes(padded, "big"), self.d, self.n).to_bytes(self.size, "big")

    def token(self, sub: str, now: int) -> str:
        enc = lambda value: b64(json.dumps(value, separators=(",", ":")).encode())
        header = {"alg": "RS256", "kid": self.kid, "typ": "at+jwt"}
        claims = {
            "iss": self.issuer,
            "aud": self.audience,
            "sub": sub,
            "scope": "openid orders",
            "iat": now,
            "nbf": now,
            "exp": now + self.ttl,
        }
        data = f"{enc(header)}.{enc(claims)}"
        return f"{data}.{b64(self.sign(data.encode()))}"

    def response(self) -> dict:
        now = int(time.time())
        return {
            "expiresAt": now + self.ttl,
            "tokens": {role: self.token(sub, now) for role, sub in SUBJECTS.items()},
            "subjects": SUBJECTS,
            "restaurants": restaurants(),
        }


def restaurants() -> list:
    try:
        return json.load(open(os.environ.get("RESTAURANTS_FILE", "/etc/demo-seed/restaurants.json")))
    except (OSError, ValueError):
        return []


ISSUER = Issuer()
ORIGINS = {origin.strip() for origin in os.environ.get("ALLOWED_ORIGINS", "").split(",") if origin.strip()}


class Handler(BaseHTTPRequestHandler):
    def cors(self) -> None:
        origin = self.headers.get("Origin")
        if origin in ORIGINS:
            self.send_header("Access-Control-Allow-Origin", origin)
            self.send_header("Access-Control-Allow-Methods", "GET, OPTIONS")
            self.send_header("Vary", "Origin")

    def do_OPTIONS(self) -> None:
        self.send_response(204)
        self.cors()
        self.end_headers()

    def do_GET(self) -> None:
        if self.path == "/healthz":
            body, status = b"ok", 200
        elif self.path == "/tokens":
            body, status = json.dumps(ISSUER.response()).encode(), 200
        else:
            body, status = b"not found", 404
        self.send_response(status)
        self.cors()
        self.send_header("Content-Type", "application/json" if self.path == "/tokens" else "text/plain")
        self.send_header("Cache-Control", "no-store")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *_args) -> None:
        pass


if __name__ == "__main__":
    ThreadingHTTPServer(("0.0.0.0", int(os.environ.get("PORT", "3011"))), Handler).serve_forever()

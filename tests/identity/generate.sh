#!/usr/bin/env bash
# Generates shared service-identity test vectors (ADR 27): a JWKS and RS256 tokens
# shaped like Kubernetes projected ServiceAccount tokens. Every bounded context
# verifies the same vectors, so the JWT checks agree across languages.
# Test-only key material; never use it outside tests.
set -euo pipefail
cd "$(dirname "$0")"
out=fixtures
mkdir -p "$out"
key="$out/signing-key.pem"
[ -f "$key" ] || openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:2048 -out "$key" 2>/dev/null
openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:2048 -out "$out/.foreign-key.pem" 2>/dev/null
python3 - "$key" "$out" <<'PY'
import base64, json, subprocess, sys
key, out = sys.argv[1], sys.argv[2]
b64 = lambda b: base64.urlsafe_b64encode(b).rstrip(b"=").decode()
mod = subprocess.run(["openssl", "rsa", "-in", key, "-noout", "-modulus"], capture_output=True, text=True, check=True).stdout.strip().split("=", 1)[1]
kid = "nomnom-test-key"
jwks = {"keys": [{"kty": "RSA", "alg": "RS256", "use": "sig", "kid": kid, "n": b64(bytes.fromhex(mod)), "e": "AQAB"}]}
json.dump(jwks, open(f"{out}/jwks.json", "w"), indent=2)
ISS = "https://kubernetes.default.svc.cluster.local"
SUB = "system:serviceaccount:nomnom:orders-worker"
FAR, PAST, IAT = 4102444800, 1700000000, 1700000000
def token(claims, signing_key=key, kid=kid):
    header = b64(json.dumps({"alg": "RS256", "kid": kid, "typ": "JWT"}, separators=(",", ":")).encode())
    payload = b64(json.dumps(claims, separators=(",", ":")).encode())
    signature = subprocess.run(["openssl", "dgst", "-sha256", "-sign", signing_key], input=f"{header}.{payload}".encode(), capture_output=True, check=True).stdout
    return f"{header}.{payload}.{b64(signature)}"
base = {"iss": ISS, "sub": SUB, "aud": ["nomnom.payments"], "iat": IAT, "nbf": IAT, "exp": FAR}
cases = {
    "valid": base,
    "valid_string_aud": {**base, "aud": "nomnom.payments"},
    "expired": {**base, "exp": PAST},
    "wrong_audience": {**base, "aud": ["nomnom.delivery"]},
    "wrong_issuer": {**base, "iss": "https://evil.example"},
    "unknown_subject": {**base, "sub": "system:serviceaccount:nomnom:stranger"},
    "not_yet_valid": {**base, "nbf": FAR - 1},
}
tokens = {name: token(claims) for name, claims in cases.items()}
tokens["bad_signature"] = token(base, signing_key=f"{out}/.foreign-key.pem")
tokens["unknown_kid"] = token(base, kid="other-key")
parts = tokens["valid"].split(".")
tokens["alg_none"] = b64(json.dumps({"alg": "none", "typ": "JWT"}).encode()) + "." + parts[1] + "."
vectors = {
    "issuer": ISS,
    "audience": "nomnom.payments",
    "service_accounts": "nomnom/orders-worker=orders-worker",
    "expected_service": "orders-worker",
    "accepted": ["valid", "valid_string_aud"],
    "rejected": [n for n in tokens if n not in ("valid", "valid_string_aud")],
    "tokens": tokens,
}
json.dump(vectors, open(f"{out}/vectors.json", "w"), indent=2)
PY
rm -f "$out/.foreign-key.pem"
echo "wrote $out/jwks.json $out/vectors.json"

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

# User assertions signed by BFF (ADR 32) and access tokens of a test identity
# provider that BFF verifies. Separate files: the service vectors above stay as they are.
bff_key="$out/bff-signing-key.pem"
[ -f "$bff_key" ] || openssl genpkey -algorithm EC -pkeyopt ec_paramgen_curve:P-256 -out "$bff_key" 2>/dev/null
openssl genpkey -algorithm EC -pkeyopt ec_paramgen_curve:P-256 -out "$out/.foreign-ec-key.pem" 2>/dev/null
idp_key="$out/idp-signing-key.pem"
[ -f "$idp_key" ] || openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:2048 -out "$idp_key" 2>/dev/null
python3 - "$key" "$bff_key" "$idp_key" "$out" <<'PY'
import base64, hashlib, hmac, json, subprocess, sys
sa_key, bff_key, idp_key, out = sys.argv[1:5]
b64 = lambda b: base64.urlsafe_b64encode(b).rstrip(b"=").decode()
def header_payload(header, claims):
    enc = lambda v: b64(json.dumps(v, separators=(",", ":")).encode())
    return f"{enc(header)}.{enc(claims)}"
def sign_rs256(signing_input, key_file):
    return subprocess.run(["openssl", "dgst", "-sha256", "-sign", key_file], input=signing_input.encode(), capture_output=True, check=True).stdout
def der_to_raw(der):
    # ECDSA-Sig-Value ::= SEQUENCE { r INTEGER, s INTEGER } -> r || s, 32 bytes each.
    assert der[0] == 0x30
    i = 2 if der[1] < 0x80 else 2 + (der[1] & 0x7F)
    parts = []
    for _ in range(2):
        assert der[i] == 0x02
        length = der[i + 1]
        parts.append(der[i + 2:i + 2 + length].lstrip(b"\x00").rjust(32, b"\x00"))
        i += 2 + length
    return b"".join(parts)
def es256(header, claims, key_file):
    signing_input = header_payload(header, claims)
    return f"{signing_input}.{b64(der_to_raw(sign_rs256(signing_input, key_file)))}"
def rs256(header, claims, key_file):
    signing_input = header_payload(header, claims)
    return f"{signing_input}.{b64(sign_rs256(signing_input, key_file))}"
def ec_jwk(key_file, kid):
    text = subprocess.run(["openssl", "ec", "-in", key_file, "-pubout", "-outform", "DER"], capture_output=True, check=True).stdout
    point = text[-65:]
    assert point[0] == 0x04
    return {"kty": "EC", "crv": "P-256", "alg": "ES256", "use": "sig", "kid": kid, "x": b64(point[1:33]), "y": b64(point[33:])}
def rsa_jwk(key_file, kid):
    mod = subprocess.run(["openssl", "rsa", "-in", key_file, "-noout", "-modulus"], capture_output=True, text=True, check=True).stdout.strip().split("=", 1)[1]
    return {"kty": "RSA", "alg": "RS256", "use": "sig", "kid": kid, "n": b64(bytes.fromhex(mod)), "e": "AQAB"}

FAR, PAST, IAT = 4102444800, 1700000000, 1700000000
SA_ISS = "https://kubernetes.default.svc.cluster.local"
AUDIENCE = "nomnom.payments"

# Service tokens of the delegate (bff) and of a workload that may not delegate.
sa_kid = "nomnom-test-key"
sa_base = {"iss": SA_ISS, "aud": [AUDIENCE], "iat": IAT, "nbf": IAT, "exp": FAR}
delegate_token = rs256({"alg": "RS256", "kid": sa_kid, "typ": "JWT"}, {**sa_base, "sub": "system:serviceaccount:nomnom:bff"}, sa_key)
non_delegate_token = rs256({"alg": "RS256", "kid": sa_kid, "typ": "JWT"}, {**sa_base, "sub": "system:serviceaccount:nomnom:orders-worker"}, sa_key)

# User assertions live at most 60 s, so they cannot stay valid in real time:
# implementations verify them with a clock fixed at NOW.
NOW = 1700000010
BFF_KID = "nomnom-bff-test-key"
bff_header = {"alg": "ES256", "kid": BFF_KID, "typ": "JWT"}
ua = {"iss": "nomnom-bff", "aud": [AUDIENCE], "sub": "customer-1", "jti": "0b6f1c1e-6d2a-4d63-9a59-1f6c7f2d3a10", "iat": NOW - 10, "nbf": NOW - 10, "exp": NOW + 20}
cases = {
    "valid": ua,
    "valid_string_aud": {**ua, "aud": AUDIENCE},
    "expired": {**ua, "iat": NOW - 120, "nbf": NOW - 120, "exp": NOW - 90},
    "not_yet_valid": {**ua, "iat": NOW + 60, "nbf": NOW + 60, "exp": NOW + 90},
    "wrong_audience": {**ua, "aud": ["nomnom.delivery"]},
    "wrong_issuer": {**ua, "iss": "https://evil.example"},
    "ttl_too_long": {**ua, "exp": NOW + 590},
    "missing_iat": {k: v for k, v in ua.items() if k != "iat"},
    "empty_sub": {**ua, "sub": ""},
    "missing_jti": {k: v for k, v in ua.items() if k != "jti"},
}
user_tokens = {name: es256(bff_header, claims, bff_key) for name, claims in cases.items()}
user_tokens["bad_signature"] = es256(bff_header, ua, f"{out}/.foreign-ec-key.pem")
user_tokens["unknown_kid"] = es256({**bff_header, "kid": "other-key"}, ua, bff_key)
user_tokens["rs256"] = rs256({"alg": "RS256", "kid": BFF_KID, "typ": "JWT"}, ua, sa_key)
hs_input = header_payload({"alg": "HS256", "kid": BFF_KID, "typ": "JWT"}, ua)
user_tokens["hs256"] = f"{hs_input}.{b64(hmac.new(b'secret', hs_input.encode(), hashlib.sha256).digest())}"
user_tokens["alg_none"] = header_payload({"alg": "none", "typ": "JWT"}, ua) + "."
json.dump({"keys": [ec_jwk(bff_key, BFF_KID)]}, open(f"{out}/bff-jwks.json", "w"), indent=2)
json.dump({
    "issuer": "nomnom-bff",
    "audience": AUDIENCE,
    "now": NOW,
    "delegates": "bff",
    "service_issuer": SA_ISS,
    "service_accounts": "nomnom/orders-worker=orders-worker,nomnom/bff=bff",
    "delegate_token": delegate_token,
    "non_delegate_token": non_delegate_token,
    "expected_subject": "customer-1",
    "accepted": ["valid", "valid_string_aud"],
    "rejected": [n for n in user_tokens if n not in ("valid", "valid_string_aud")],
    "tokens": user_tokens,
}, open(f"{out}/user-vectors.json", "w"), indent=2)

# Access tokens of a test identity provider for BFF. An ID token (nonce, no
# scope) must not be accepted as an access token.
IDP_KID = "nomnom-idp-test-key"
idp_header = {"alg": "RS256", "kid": IDP_KID, "typ": "at+jwt"}
at = {"iss": "https://idp.nomnom.test", "aud": "nomnom-bff", "sub": "customer-1", "scope": "openid orders", "iat": IAT, "nbf": IAT, "exp": FAR}
idp_cases = {
    "valid": at,
    "expired": {**at, "exp": PAST},
    "wrong_audience": {**at, "aud": "other-api"},
    "wrong_issuer": {**at, "iss": "https://evil.example"},
    "id_token": {**{k: v for k, v in at.items() if k != "scope"}, "nonce": "n-0S6_WzA2Mj"},
}
idp_tokens = {name: rs256(idp_header, claims, idp_key) for name, claims in idp_cases.items()}
idp_tokens["bad_signature"] = rs256(idp_header, at, sa_key)
json.dump({"keys": [rsa_jwk(idp_key, IDP_KID)]}, open(f"{out}/idp-jwks.json", "w"), indent=2)
json.dump({
    "issuer": "https://idp.nomnom.test",
    "audience": "nomnom-bff",
    "expected_subject": "customer-1",
    "accepted": ["valid"],
    "rejected": [n for n in idp_tokens if n != "valid"],
    "tokens": idp_tokens,
}, open(f"{out}/idp-vectors.json", "w"), indent=2)
PY
rm -f "$out/.foreign-key.pem" "$out/.foreign-ec-key.pem"
echo "wrote $out/jwks.json $out/vectors.json $out/bff-jwks.json $out/user-vectors.json $out/idp-jwks.json $out/idp-vectors.json"

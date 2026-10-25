"""ADR 27, ADR 32: публикует JWKS для проверки identity в неймспейсах bounded contexts.

- cluster-jwks — JWKS ServiceAccount issuer кластера (/openid/v1/jwks): projected tokens;
- bff-jwks — публичный ключ user assertions BFF из сертификата cert-manager (Secret
  bff-assertion-key; используется только tls.crt).

Выполняется Job identity-jwks (templates/identity.yaml) при каждом helm upgrade. Только stdlib:
Kubernetes API вызывается токеном ServiceAccount pod, ConfigMap записываются server-side apply.
"""

import base64
import json
import os
import ssl
import sys
import time
import urllib.error
import urllib.request

SERVICE_ACCOUNT = "/var/run/secrets/kubernetes.io/serviceaccount"
# SubjectPublicKeyInfo P-256: OID prime256v1, затем BIT STRING (0x03 0x42 0x00) с несжатой
# точкой 0x04 || x || y. Подпись сертификата использует другой OID (ecdsa-with-SHA256).
P256_POINT_PREFIX = bytes.fromhex("06082a8648ce3d030107034200")


def request(method, path, body=None):
    host = os.environ["KUBERNETES_SERVICE_HOST"]
    api = f"https://{f'[{host}]' if ':' in host else host}:{os.environ['KUBERNETES_SERVICE_PORT']}"
    tls = ssl.create_default_context(cafile=f"{SERVICE_ACCOUNT}/ca.crt")
    with open(f"{SERVICE_ACCOUNT}/token") as token:
        # /openid/v1/jwks отвечает application/jwk-set+json: только application/json даёт 406.
        headers = {"Authorization": f"Bearer {token.read().strip()}", "Accept": "application/json, application/jwk-set+json"}
    data = None
    if body is not None:
        headers["Content-Type"] = "application/apply-patch+yaml"
        data = json.dumps(body).encode()
    call = urllib.request.Request(api + path, data=data, method=method, headers=headers)
    with urllib.request.urlopen(call, context=tls, timeout=30) as response:
        return response.read()


def bff_certificate(namespace, name, timeout):
    """tls.crt Secret, который выпускает cert-manager; ждёт выпуска до timeout секунд."""
    deadline = time.monotonic() + timeout
    while True:
        try:
            data = json.loads(request("GET", f"/api/v1/namespaces/{namespace}/secrets/{name}")).get("data") or {}
            if data.get("tls.crt"):
                return base64.b64decode(data["tls.crt"]).decode()
        except urllib.error.HTTPError as error:
            if error.code != 404:
                raise
        if time.monotonic() > deadline:
            sys.exit(f"{namespace}/{name}: tls.crt is not issued by cert-manager after {timeout}s")
        time.sleep(2)


def bff_jwks(certificate_pem, kid):
    body = certificate_pem.split("-----BEGIN CERTIFICATE-----", 1)[1].split("-----END CERTIFICATE-----", 1)[0]
    der = base64.b64decode("".join(body.split()))
    index = der.find(P256_POINT_PREFIX)
    point = der[index + len(P256_POINT_PREFIX) :][:65] if index >= 0 else b""
    if len(point) != 65 or point[0] != 0x04:
        sys.exit("bff-assertion-key: expected an uncompressed P-256 public key")

    def b64(raw):
        return base64.urlsafe_b64encode(raw).rstrip(b"=").decode()

    key = {"kty": "EC", "crv": "P-256", "alg": "ES256", "use": "sig", "kid": kid, "x": b64(point[1:33]), "y": b64(point[33:])}
    return json.dumps({"keys": [key]})


def publish(namespace, name, jwks):
    manifest = {
        "apiVersion": "v1",
        "kind": "ConfigMap",
        "metadata": {"name": name, "namespace": namespace, "labels": {"app.kubernetes.io/part-of": "nomnom-kind"}},
        "data": {"jwks.json": jwks},
    }
    path = f"/api/v1/namespaces/{namespace}/configmaps/{name}?fieldManager=kind-bootstrap&force=true"
    request("PATCH", path, manifest)


def main():
    expected_issuer = os.environ["EXPECTED_ISSUER"]
    issuer = json.loads(request("GET", "/.well-known/openid-configuration"))["issuer"]
    if issuer != expected_issuer:
        sys.exit(f"cluster issuer {issuer} differs from {expected_issuer} in ops/kind/values")
    cluster = request("GET", "/openid/v1/jwks").decode()
    certificate = bff_certificate(os.environ["BFF_NAMESPACE"], os.environ["BFF_SECRET"], 300)
    bff = bff_jwks(certificate, os.environ["BFF_KEY_ID"])
    namespaces = os.environ["JWKS_NAMESPACES"].split()
    for namespace in namespaces:
        publish(namespace, "cluster-jwks", cluster)
        publish(namespace, "bff-jwks", bff)
    print(f"cluster-jwks (issuer {issuer}) and bff-jwks published to {' '.join(namespaces)}")


if __name__ == "__main__":
    main()

#!/usr/bin/env bash
# ADR 32: identity BFF в тестовом kind-кластере.
#   - Secret bff-assertion-key (nomnom-bff, ключ key.pem): ключ подписи user assertions (P-256).
#     Создаётся один раз на кластер и хранится только в Secret; повторный запуск его не меняет.
#   - ConfigMap bff-jwks (jwks.json) в namespaces bounded contexts: публичный ключ BFF для
#     IDENTITY__USER_JWKS_FILE; каждый запуск переиздаёт его из ключа в Secret.
#   - ConfigMap idp-jwks (nomnom-bff): JWKS тестового identity provider из tests/identity/fixtures.
#     Реального provider в kind нет: проверки выпускают access tokens тестовым ключом из того же
#     каталога. ТОЛЬКО для kind.
# Вызывается hook'ом Skaffold рядом с identity-jwks.sh, после создания namespaces.
set -euo pipefail
context="${1:?usage: bff-identity.sh <kube-context>}"
root="$(cd "$(dirname "$0")/../../.." && pwd)"
k() { kubectl --context "${context}" "$@"; }

# LibreSSL пишет EC-ключи с явными параметрами кривой, которые Go не принимает:
# при наличии берётся OpenSSL 3 (как в tests/identity/generate.sh).
if openssl version | grep -q LibreSSL; then
  for candidate in /opt/homebrew/opt/openssl@3/bin /usr/local/opt/openssl@3/bin; do
    if [[ -x "${candidate}/openssl" ]]; then PATH="${candidate}:${PATH}"; break; fi
  done
fi

# kid совпадает с identity.assertionKeyId в ops/kind/values/bff.yaml.
kid="nomnom-bff-kind"
work="$(mktemp -d)"
trap 'rm -rf "${work}"' EXIT

if k -n nomnom-bff get secret bff-assertion-key -o jsonpath='{.data.key\.pem}' 2>/dev/null |
  base64 -d >"${work}/key.pem" 2>/dev/null && [[ -s "${work}/key.pem" ]]; then
  state="kept"
else
  openssl genpkey -algorithm EC -pkeyopt ec_paramgen_curve:P-256 -pkeyopt ec_param_enc:named_curve \
    -out "${work}/key.pem" 2>/dev/null
  k -n nomnom-bff create secret generic bff-assertion-key --from-file=key.pem="${work}/key.pem" \
    --dry-run=client -o yaml | k apply -f - >/dev/null
  state="created"
fi

openssl pkey -in "${work}/key.pem" -pubout -outform DER >"${work}/public.der" 2>/dev/null
python3 -I - "${work}/public.der" "${kid}" >"${work}/jwks.json" <<'PY'
import base64, json, sys
# SubjectPublicKeyInfo P-256 заканчивается несжатой точкой 0x04 || x || y.
point = open(sys.argv[1], "rb").read()[-65:]
assert point[0] == 0x04, "expected an uncompressed P-256 point"
b64 = lambda b: base64.urlsafe_b64encode(b).rstrip(b"=").decode()
print(json.dumps({"keys": [{"kty": "EC", "crv": "P-256", "alg": "ES256", "use": "sig",
                            "kid": sys.argv[2], "x": b64(point[1:33]), "y": b64(point[33:])}]}))
PY

for namespace in nomnom-orders nomnom-restaurants nomnom-delivery nomnom-payments; do
  k -n "${namespace}" create configmap bff-jwks --from-file=jwks.json="${work}/jwks.json" \
    --dry-run=client -o yaml | k apply -f - >/dev/null
done
k -n nomnom-bff create configmap idp-jwks \
  --from-file=jwks.json="${root}/tests/identity/fixtures/idp-jwks.json" \
  --dry-run=client -o yaml | k apply -f - >/dev/null

# ServiceAccount nomnom-bff/bff принадлежит chart BFF. На кластере, где его создала прежняя
# фикстура grpc-caller, он передаётся release bff, иначе helm install откажет.
if k -n nomnom-bff get serviceaccount bff >/dev/null 2>&1 &&
  [[ -z "$(k -n nomnom-bff get serviceaccount bff -o jsonpath='{.metadata.annotations.meta\.helm\.sh/release-name}')" ]]; then
  k -n nomnom-bff annotate serviceaccount bff --overwrite \
    meta.helm.sh/release-name=bff meta.helm.sh/release-namespace=nomnom-bff >/dev/null
  k -n nomnom-bff label serviceaccount bff --overwrite app.kubernetes.io/managed-by=Helm >/dev/null
fi
echo "bff-assertion-key ${state} (kid ${kid}); bff-jwks and idp-jwks published"

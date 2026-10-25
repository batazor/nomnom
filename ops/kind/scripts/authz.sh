#!/usr/bin/env bash
# ADR 18: публикует authorization/schema.zed и bootstrap.yaml ConfigMap'ом для SpiceDB и
# создаёт случайный preshared key (Secret spicedb-preshared-key) в nomnom-authz и в namespaces
# bounded contexts. Существующий ключ сохраняется, чтобы повторный deploy не рвал клиентов.
set -euo pipefail
context="${1:?usage: authz.sh <kube-context>}"
root="$(cd "$(dirname "$0")/../../.." && pwd)"
k() { kubectl --context "${context}" "$@"; }

k -n nomnom-authz create configmap spicedb-bootstrap \
  --from-file=schema.zed="${root}/authorization/schema.zed" \
  --from-file=bootstrap.yaml="${root}/authorization/bootstrap.yaml" \
  --dry-run=client -o yaml | k apply -f - >/dev/null

key="$(k -n nomnom-authz get secret spicedb-preshared-key -o jsonpath='{.data.preshared-key}' 2>/dev/null | base64 -d || true)"
[[ -n "${key}" ]] || key="$(openssl rand -hex 32)"
for namespace in nomnom-authz nomnom-orders nomnom-restaurants nomnom-delivery nomnom-payments; do
  k -n "${namespace}" create secret generic spicedb-preshared-key --from-literal=preshared-key="${key}" \
    --dry-run=client -o yaml | k apply -f - >/dev/null
done
# Новый bootstrap применяется только при старте memory datastore. Checksum в pod template
# перезапускает SpiceDB лишь при изменении schema/bootstrap: безусловный restart на каждой
# итерации skaffold dev стирал бы relationships, записанные приложениями.
checksum="$(cat "${root}/authorization/schema.zed" "${root}/authorization/bootstrap.yaml" | shasum -a 256 | cut -c1-16)"
k -n nomnom-authz patch deployment/spicedb --type merge \
  -p "{\"spec\":{\"template\":{\"metadata\":{\"annotations\":{\"nomnom.dev/bootstrap-checksum\":\"${checksum}\"}}}}}" \
  >/dev/null 2>&1 || true
echo "spicedb bootstrap and preshared key published"

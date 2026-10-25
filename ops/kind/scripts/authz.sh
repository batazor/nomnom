#!/usr/bin/env bash
# ADR 18: публикует authorization/schema.zed и bootstrap.yaml (вместе с kind-only grants
# ops/kind/authz/bootstrap-kind.yaml) ConfigMap'ом для SpiceDB и
# создаёт случайный preshared key (Secret spicedb-preshared-key) в nomnom-authz и в namespaces
# bounded contexts. Существующий ключ сохраняется, чтобы повторный deploy не рвал клиентов.
set -euo pipefail
context="${1:?usage: authz.sh <kube-context>}"
root="$(cd "$(dirname "$0")/../../.." && pwd)"
k() { kubectl --context "${context}" "$@"; }

# Bootstrap kind = общий authorization/bootstrap.yaml + relationships ops/kind/authz/
# bootstrap-kind.yaml (operator платформы), дописанные в конец его блока relationships.
bootstrap="$(mktemp)"
trap 'rm -f "${bootstrap}"' EXIT
kind_relationships="$(sed -n '/^relationships: |-$/,/^[^ ]/p' "${root}/ops/kind/authz/bootstrap-kind.yaml" | grep '^  ')"
awk -v extra="${kind_relationships}" '
  block && !/^  / { print extra; block = 0; merged = 1 }
  { print }
  /^relationships: \|-$/ { block = 1 }
  END {
    if (block) { print extra; merged = 1 }
    if (!merged) { print "authz.sh: no relationships block in authorization/bootstrap.yaml" > "/dev/stderr"; exit 1 }
  }
' "${root}/authorization/bootstrap.yaml" >"${bootstrap}"

k -n nomnom-authz create configmap spicedb-bootstrap \
  --from-file=schema.zed="${root}/authorization/schema.zed" \
  --from-file=bootstrap.yaml="${bootstrap}" \
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
checksum="$(cat "${root}/authorization/schema.zed" "${bootstrap}" | shasum -a 256 | cut -c1-16)"
k -n nomnom-authz patch deployment/spicedb --type merge \
  -p "{\"spec\":{\"template\":{\"metadata\":{\"annotations\":{\"nomnom.dev/bootstrap-checksum\":\"${checksum}\"}}}}}" \
  >/dev/null 2>&1 || true
echo "spicedb bootstrap and preshared key published"

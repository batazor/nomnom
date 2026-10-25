#!/usr/bin/env bash
# ADR 27: публикует JWKS кластера ConfigMap'ом cluster-jwks в namespaces bounded contexts,
# чтобы их SubjectResolver проверял projected ServiceAccount tokens без обращения к Kubernetes API.
# Вызывается hook'ом Skaffold после создания namespaces; повторный запуск обновляет ConfigMap
# (например, после ротации signing key кластера).
set -euo pipefail
context="${1:?usage: identity-jwks.sh <kube-context>}"
k() { kubectl --context "${context}" "$@"; }

expected_issuer="https://kubernetes.default.svc.cluster.local"
issuer="$(k get --raw /.well-known/openid-configuration | sed -n 's/.*"issuer":"\([^"]*\)".*/\1/p')"
[[ "${issuer}" == "${expected_issuer}" ]] ||
  { echo "cluster issuer ${issuer} differs from ${expected_issuer} in ops/kind/values" >&2; exit 1; }

jwks="$(mktemp)"
trap 'rm -f "${jwks}"' EXIT
k get --raw /openid/v1/jwks >"${jwks}"
for namespace in nomnom-orders nomnom-restaurants nomnom-delivery nomnom-payments; do
  k -n "${namespace}" create configmap cluster-jwks --from-file=jwks.json="${jwks}" \
    --dry-run=client -o yaml | k apply -f - >/dev/null
done
echo "cluster-jwks published (issuer ${issuer})"

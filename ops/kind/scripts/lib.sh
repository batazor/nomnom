#!/usr/bin/env bash
# Общие переменные и функции тестового kind-окружения. Подключается через source.
set -euo pipefail

KIND_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
REPO_ROOT="$(cd "${KIND_DIR}/../.." && pwd)"

# shellcheck source=../versions.env
source "${KIND_DIR}/versions.env"

KIND_CLUSTER_NAME="${KIND_CLUSTER_NAME:-nomnom-test}"
KUBE_CONTEXT="kind-${KIND_CLUSTER_NAME}"
WEBHOOKS_HOST_PORT="${WEBHOOKS_HOST_PORT:-18085}"
ARTIFACTS_DIR="${ARTIFACTS_DIR:-${KIND_DIR}/.artifacts}"

APP_NAMESPACES=(nomnom-orders nomnom-restaurants nomnom-delivery nomnom-payments nomnom-controlplane nomnom-bff)
GATEWAY_NAMESPACE=nomnom-gateway
GATEWAY_SYSTEM_NAMESPACE=envoy-gateway-system
INTERNAL_GATEWAY_ADDR="internal-gateway.${GATEWAY_SYSTEM_NAMESPACE}.svc.cluster.local:8080"

log() {
  printf '[kind %s] %s\n' "$(date +%H:%M:%S)" "$*" >&2
}

die() {
  printf '[kind] ERROR: %s\n' "$*" >&2
  exit 1
}

require_command() {
  local command
  for command in "$@"; do
    command -v "${command}" >/dev/null 2>&1 || die "required command is missing: ${command}"
  done
}

cluster_exists() {
  kind get clusters 2>/dev/null | grep -Fxq "${KIND_CLUSTER_NAME}"
}

# Все обращения к API идут только в контекст тестового kind-кластера.
assert_test_context() {
  [[ "${KUBE_CONTEXT}" == kind-* ]] || die "refusing non-kind context ${KUBE_CONTEXT}"
  cluster_exists || die "kind cluster ${KIND_CLUSTER_NAME} does not exist; run make kind-up"
}

k() {
  kubectl --context "${KUBE_CONTEXT}" "$@"
}

#!/usr/bin/env bash
# Полный неинтерактивный прогон для CI: кластер → skaffold run → проверки → диагностика
# при ошибке → удаление (KIND_KEEP=1 сохраняет кластер).
source "$(dirname "$0")/lib.sh"

scripts="${KIND_DIR}/scripts"

on_exit() {
  local status=$?
  if ((status != 0)); then
    log "run failed with status ${status}, collecting diagnostics"
    "${scripts}/diagnostics.sh" || true
  fi
  if [[ "${KIND_KEEP:-0}" != 1 ]]; then
    kind delete cluster --name "${KIND_CLUSTER_NAME}" || true
  fi
  exit "${status}"
}
trap on_exit EXIT

make -C "${KIND_DIR}/../.." kind-cluster
(cd "${KIND_DIR}/skaffold" && GATEWAY_API_VERSION="${GATEWAY_API_VERSION}" \
  skaffold run --kube-context "${KUBE_CONTEXT}" --status-check=true)
# Тот же набор проверок, что make kind-test: verify.sh и Chainsaw (ops/kind/e2e).
make -C "${KIND_DIR}/../.." kind-test

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
    "${scripts}/cluster.sh" delete || true
  fi
  exit "${status}"
}
trap on_exit EXIT

"${scripts}/cluster.sh" create
(cd "${KIND_DIR}/skaffold" && GATEWAY_API_VERSION="${GATEWAY_API_VERSION}" \
  skaffold run --kube-context "${KUBE_CONTEXT}" --status-check=true)
"${scripts}/verify.sh"

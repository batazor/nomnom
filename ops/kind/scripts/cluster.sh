#!/usr/bin/env bash
# Создание и удаление тестового kind-кластера. Платформу и приложения ставит Skaffold.
source "$(dirname "$0")/lib.sh"

require_command kind docker kubectl helm skaffold

check_versions() {
  local kind_actual skaffold_actual
  kind_actual="$(kind version | awk '{print $2}')"
  [[ "${kind_actual}" == "${KIND_VERSION}" ]] ||
    die "kind ${KIND_VERSION} is required by versions.env, found ${kind_actual}"
  skaffold_actual="$(skaffold version)"
  [[ "${skaffold_actual}" == "${SKAFFOLD_VERSION}" ]] ||
    die "skaffold ${SKAFFOLD_VERSION} is required by versions.env, found ${skaffold_actual}"
}

# kind load docker-image импортирует все платформы multi-arch index и падает, если в Docker
# хоста есть только платформа узла. Поэтому образ сохраняется для платформы узла, а для
# ссылок вида repo:tag@sha256:<index> в containerd добавляется ref с исходным digest:
# содержимое скачано Docker именно по этому digest.
load_platform_image() {
  local image="$1" tagged="${1%@*}" digest="" node arch
  [[ "${image}" == *@sha256:* ]] && digest="${image##*@}"
  docker image inspect "${image}" >/dev/null 2>&1 || docker pull --quiet "${image}" >/dev/null
  [[ -n "${digest}" ]] && docker tag "${image}" "${tagged}"
  for node in $(kind get nodes --name "${KIND_CLUSTER_NAME}"); do
    arch="$(docker exec "${node}" uname -m)"
    case "${arch}" in
      aarch64) arch=arm64 ;;
      x86_64) arch=amd64 ;;
    esac
    docker save --platform "linux/${arch}" "${tagged}" |
      docker exec -i "${node}" ctr --namespace=k8s.io images import --snapshotter=overlayfs - >/dev/null
    if [[ -n "${digest}" ]]; then
      docker exec "${node}" ctr --namespace=k8s.io images tag --force "${tagged}" "${tagged%:*}@${digest}" >/dev/null
    fi
  done
}

# Образы платформы скачиваются один раз в Docker хоста и загружаются в узел: кеш хоста
# переживает удаление кластера, поэтому повторные прогоны не тянут их из сети. Список
# берётся из рендера закреплённых в Skaffold charts.
preload_images() {
  local image
  (cd "${KIND_DIR}/skaffold" && skaffold render --module platform 2>/dev/null) |
    sed -nE 's/^[[:space:]]*image:[[:space:]]*"?([^"[:space:]]+)"?.*/\1/p' | sort -u | grep -v ratelimit |
    { cat; printf '%s\n%s\n' "${ENVOY_PROXY_IMAGE}" "${PROBE_IMAGE}"; } |
    while IFS= read -r image; do
      log "preloading ${image}"
      load_platform_image "${image}"
    done
}

create() {
  check_versions
  if cluster_exists; then
    log "cluster ${KIND_CLUSTER_NAME} already exists, reusing it"
  else
    mkdir -p "${ARTIFACTS_DIR}"
    local config="${ARTIFACTS_DIR}/cluster.${KIND_CLUSTER_NAME}.yaml"
    sed -e "s|__BFF_HOST_PORT__|${BFF_HOST_PORT}|" -e "s|__WEBHOOKS_HOST_PORT__|${WEBHOOKS_HOST_PORT}|" "${KIND_DIR}/cluster.yaml" >"${config}"
    log "creating cluster ${KIND_CLUSTER_NAME} (${KIND_NODE_IMAGE%@*})"
    kind create cluster --name "${KIND_CLUSTER_NAME}" --image "${KIND_NODE_IMAGE}" --config "${config}"
  fi

  if k -n kube-system get daemonset kindnet >/dev/null 2>&1; then
    die "cluster ${KIND_CLUSTER_NAME} uses kindnet; delete it and recreate from cluster.yaml"
  fi
  local nodes
  nodes="$(k get nodes --no-headers | wc -l | tr -d ' ')"
  [[ "${nodes}" == 1 ]] || die "expected a single-node cluster, found ${nodes} nodes"

  if [[ "${PRELOAD_PLATFORM_IMAGES:-1}" == 1 ]]; then
    preload_images
  fi
}

delete() {
  if cluster_exists; then
    log "deleting cluster ${KIND_CLUSTER_NAME}"
    kind delete cluster --name "${KIND_CLUSTER_NAME}"
  else
    log "cluster ${KIND_CLUSTER_NAME} does not exist"
  fi
}

case "${1:-}" in
  create) create ;;
  delete) delete ;;
  *) die "usage: cluster.sh create|delete" ;;
esac

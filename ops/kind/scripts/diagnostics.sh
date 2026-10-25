#!/usr/bin/env bash
# Сбор диагностики: pod logs, events, состояние Gateway API и Cilium/Hubble.
source "$(dirname "$0")/lib.sh"

require_command kubectl
if ! cluster_exists; then
  log "cluster ${KIND_CLUSTER_NAME} does not exist, nothing to collect"
  exit 0
fi

out="${ARTIFACTS_DIR}/diagnostics-$(date +%Y%m%d-%H%M%S)"
mkdir -p "${out}/logs"
log "collecting diagnostics into ${out}"

namespaces=("${APP_NAMESPACES[@]}" nomnom-gateway nomnom-outsider "${GATEWAY_SYSTEM_NAMESPACE}" kube-system)

{
  k get nodes -o wide
  k get pods -A -o wide
  k get svc,endpointslices -A
} >"${out}/cluster.txt" 2>&1 || true
k get events -A --sort-by=.lastTimestamp >"${out}/events.txt" 2>&1 || true
k get networkpolicy -A -o yaml >"${out}/networkpolicies.yaml" 2>&1 || true
k get gatewayclass,gateway,grpcroute,referencegrant,backendtrafficpolicy,envoyproxy -A -o yaml \
  >"${out}/gateway-api.yaml" 2>&1 || true

for namespace in "${namespaces[@]}"; do
  k -n "${namespace}" describe pods >"${out}/describe-${namespace}.txt" 2>&1 || true
  for pod in $(k -n "${namespace}" get pods -o name 2>/dev/null); do
    name="${namespace}_${pod#pod/}"
    k -n "${namespace}" logs "${pod}" --all-containers --timestamps >"${out}/logs/${name}.log" 2>&1 || true
    k -n "${namespace}" logs "${pod}" --all-containers --previous >"${out}/logs/${name}.previous.log" 2>/dev/null ||
      rm -f "${out}/logs/${name}.previous.log"
  done
done

for pod in $(k -n kube-system get pods -l k8s-app=cilium -o name 2>/dev/null); do
  name="${pod#pod/}"
  {
    k -n kube-system exec "${pod}" -c cilium-agent -- cilium-dbg status --verbose
    k -n kube-system exec "${pod}" -c cilium-agent -- cilium-dbg endpoint list
    k -n kube-system exec "${pod}" -c cilium-agent -- hubble observe --last 500 --verdict DROPPED
  } >"${out}/cilium-${name}.txt" 2>&1 || true
done

log "diagnostics saved to ${out}"

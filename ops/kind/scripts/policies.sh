#!/usr/bin/env bash
# Применение и удаление NetworkPolicy, запрещающих обход internal gateway.
source "$(dirname "$0")/lib.sh"

require_command kubectl
assert_test_context

netpol="${KIND_DIR}/manifests/netpol"

apply() {
  local namespace
  for namespace in "${APP_NAMESPACES[@]}"; do
    k -n "${namespace}" apply -f "${netpol}/app-baseline.yaml"
  done
  k apply -f "${netpol}/app-infrastructure.yaml" -f "${netpol}/gateway.yaml"
}

delete() {
  local namespace
  for namespace in "${APP_NAMESPACES[@]}"; do
    k -n "${namespace}" delete -f "${netpol}/app-baseline.yaml" --ignore-not-found
  done
  k delete -f "${netpol}/app-infrastructure.yaml" -f "${netpol}/gateway.yaml" --ignore-not-found
}

case "${1:-}" in
  apply) apply ;;
  delete) delete ;;
  *) die "usage: policies.sh apply|delete" ;;
esac

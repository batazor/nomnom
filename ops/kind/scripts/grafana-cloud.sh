#!/usr/bin/env bash
# ADR 9: Alloy → Grafana Cloud для Skaffold profile grafana-cloud. Читает
# ops/grafana-cloud/.env.grafana-cloud, публикует Secret grafana-cloud-otlp и применяет
# manifests/observability/alloy.yaml. Без endpoint, instance ID или token падает сразу:
# Alloy без них отбрасывал бы метрики молча.
set -euo pipefail
context="${1:?usage: grafana-cloud.sh <kube-context>}"
root="$(cd "$(dirname "$0")/../../.." && pwd)"
env_file="${GRAFANA_CLOUD_ENV_FILE:-${root}/ops/grafana-cloud/.env.grafana-cloud}"
k() { kubectl --context "${context}" "$@"; }

[[ -f "${env_file}" ]] || { echo "grafana-cloud: ${env_file} not found, copy .env.grafana-cloud.example" >&2; exit 1; }
# shellcheck disable=SC1090
source "${env_file}"
for name in GRAFANA_CLOUD_OTLP_ENDPOINT GRAFANA_CLOUD_OTLP_INSTANCE_ID GRAFANA_CLOUD_OTLP_TOKEN; do
  [[ -n "${!name:-}" ]] || { echo "grafana-cloud: ${name} is empty in ${env_file}" >&2; exit 1; }
done

manifest="${root}/ops/kind/manifests/observability/alloy.yaml"
k apply -f "${manifest}" >/dev/null
k -n nomnom-observability create secret generic grafana-cloud-otlp \
  --from-literal=GRAFANA_CLOUD_OTLP_ENDPOINT="${GRAFANA_CLOUD_OTLP_ENDPOINT}" \
  --from-literal=GRAFANA_CLOUD_OTLP_INSTANCE_ID="${GRAFANA_CLOUD_OTLP_INSTANCE_ID}" \
  --from-literal=GRAFANA_CLOUD_OTLP_TOKEN="${GRAFANA_CLOUD_OTLP_TOKEN}" \
  --dry-run=client -o yaml | k apply -f - >/dev/null
# DSN экспортеров PostgreSQL Alloy: пароли владельцев публикует postgres.sh (идемпотентно).
"$(dirname "$0")/postgres.sh" "${context}" >/dev/null
dsn_args=()
for owner in orders payments delivery; do
  password="$(k -n nomnom-data get secret "postgres-${owner}" -o jsonpath='{.data.password}' | base64 -d)"
  upper="$(printf '%s' "${owner}" | tr '[:lower:]' '[:upper:]')"
  dsn_args+=("--from-literal=POSTGRES_${upper}_DSN=postgres://${owner}:${password}@postgres-${owner}.nomnom-data.svc.cluster.local:5432/${owner}?sslmode=disable")
done
k -n nomnom-observability create secret generic alloy-postgres "${dsn_args[@]}" \
  --dry-run=client -o yaml | k apply -f - >/dev/null
# Secret и config.alloy читаются при старте процесса.
k -n nomnom-observability rollout restart deployment/alloy >/dev/null
k -n nomnom-observability rollout status deployment/alloy --timeout=180s >/dev/null
echo "alloy → grafana cloud published"

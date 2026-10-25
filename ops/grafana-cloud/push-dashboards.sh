#!/usr/bin/env bash
# ADR 29: загрузка overview-dashboards в stack Grafana Cloud через gcx для проверки на данных
# kind. Stack и service account token берутся из .env.grafana-cloud; gcx config временный,
# token gcx хранит в системном keychain.
set -euo pipefail
root="$(cd "$(dirname "$0")/../.." && pwd)"
env_file="${GRAFANA_CLOUD_ENV_FILE:-${root}/ops/grafana-cloud/.env.grafana-cloud}"
gcx="${GCX:-gcx}"

[[ -f "${env_file}" ]] || { echo "grafana-cloud: ${env_file} not found, copy .env.grafana-cloud.example" >&2; exit 1; }
# shellcheck disable=SC1090
source "${env_file}"
for name in GRAFANA_CLOUD_STACK_URL GRAFANA_CLOUD_SA_TOKEN; do
  [[ -n "${!name:-}" ]] || { echo "grafana-cloud: ${name} is empty in ${env_file}" >&2; exit 1; }
done

config_dir="$(mktemp -d)"
trap 'rm -rf "${config_dir}"' EXIT
config="${config_dir}/config.yaml"
echo '{}' >"${config}"
g() { "${gcx}" --config "${config}" "$@"; }
g config set stacks.nomnom.grafana.server "${GRAFANA_CLOUD_STACK_URL}" >/dev/null
g config set stacks.nomnom.grafana.token "${GRAFANA_CLOUD_SA_TOKEN}" >/dev/null
g config set contexts.nomnom.stack nomnom >/dev/null
g config set current-context nomnom >/dev/null

# Editor в Grafana 13 создаёт dashboards в корне, но не читает и не обновляет их: dashboards
# кладутся в folder nomnom, на который создатель получает права администратора.
folder=nomnom
status="$(curl -s -o /dev/null -w '%{http_code}' -H "Authorization: Bearer ${GRAFANA_CLOUD_SA_TOKEN}" \
  -H 'Content-Type: application/json' -d "{\"uid\":\"${folder}\",\"title\":\"nomnom\"}" \
  "${GRAFANA_CLOUD_STACK_URL%/}/api/folders")"
[[ "${status}" == 200 || "${status}" == 409 || "${status}" == 412 ]] || { echo "grafana-cloud: create folder: HTTP ${status}" >&2; exit 1; }

# Артефакты не меняются: annotation folder добавляется во временные копии.
mkdir "${config_dir}/dashboards"
for source in "${root}"/boundaries/*/ops/grafana/dashboards/overview.json; do
  service="$(basename "$(dirname "$(dirname "$(dirname "$(dirname "${source}")")")")")"
  jq --arg folder "${folder}" '.metadata.annotations["grafana.app/folder"] = $folder' "${source}" \
    >"${config_dir}/dashboards/${service}.json"
done
jq --arg folder "${folder}" '.metadata.annotations["grafana.app/folder"] = $folder' \
  "${root}/ops/grafana/dashboards/platform.json" >"${config_dir}/dashboards/platform.json"
g resources push -p "${config_dir}/dashboards" -o text

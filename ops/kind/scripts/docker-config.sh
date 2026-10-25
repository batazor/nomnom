#!/usr/bin/env bash
# Docker config для Skaffold, Helm и docker kind-окружения без credential helper: в Docker
# Desktop docker-credential-desktop может не отвечать, и pull/build, buildx imagetools и OCI
# helm charts висят без ошибки. Все образы и charts окружения публичные, credentials не нужны.
# Создаёт ARTIFACTS_DIR/docker с config.json без credsStore/credHelpers и выводит путь;
# контекст Docker и CLI plugins (buildx) берутся из пользовательского ~/.docker.
set -euo pipefail

KIND_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
dir="${ARTIFACTS_DIR:-${KIND_DIR}/.artifacts}/docker"
user_dir="${HOME}/.docker"

mkdir -p "${dir}"
rm -rf "${dir}/contexts"
if [[ -d "${user_dir}/contexts" ]]; then
  cp -R "${user_dir}/contexts" "${dir}/contexts"
fi

context=""
if [[ -f "${user_dir}/config.json" ]]; then
  context="$(jq -r '.currentContext // ""' "${user_dir}/config.json")"
fi

# Пустой DOCKER_CONFIG теряет buildx: Skaffold падает с «buildx component is missing».
jq -n --arg context "${context}" \
  --arg user_plugins "${user_dir}/cli-plugins" \
  --arg desktop_plugins /Applications/Docker.app/Contents/Resources/cli-plugins \
  '{cliPluginsExtraDirs: [$user_plugins, $desktop_plugins]}
   + (if $context == "" then {} else {currentContext: $context} end)' >"${dir}/config.json"

echo "${dir}"

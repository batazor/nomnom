#!/usr/bin/env bash
# Сохраняет корневой сертификат локального CA в ops/kind/.artifacts/nomnom-local-ca.crt
# для клиентов public gateway: curl --cacert <файл> https://webhooks.nomnom.localhost:...
source "$(dirname "$0")/lib.sh"

require_command kubectl
assert_test_context

mkdir -p "${ARTIFACTS_DIR}"
out="${ARTIFACTS_DIR}/nomnom-local-ca.crt"
k -n cert-manager get secret nomnom-local-ca -o jsonpath='{.data.ca\.crt}' | base64 -d >"${out}"
echo "${out}"

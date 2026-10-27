#!/bin/sh
# Тело persisted-запроса к BFF (локальный ADR 3 BFF): BFF принимает только операции своего
# manifest, поэтому e2e шлют не текст, а SHA-256 текста операции ops/kind/e2e/operations/<имя>.graphql
# в extensions.persistedQuery. Текст хешируется без завершающих переводов строки — так же, как
# его кладёт в boundaries/bff/persisted-operations скрипт boundaries/bff/scripts/persisted-operations.mjs.
#
# Использование: graphql-body.sh <имя операции> <variables как JSON-объект>
set -eu
[ "$#" -eq 2 ] || { echo "usage: $0 <operation> <variables-json>" >&2; exit 2; }
file="$(git rev-parse --show-toplevel)/ops/kind/e2e/operations/$1.graphql"
[ -f "$file" ] || { echo "no operation $file" >&2; exit 1; }
text="$(cat "$file")"
hash="$(printf '%s' "$text" | python3 -I -c 'import hashlib, sys; print(hashlib.sha256(sys.stdin.buffer.read()).hexdigest())')"
jq -cn --arg name "$1" --arg hash "$hash" --argjson variables "$2" \
  '{operationName: $name, variables: $variables, extensions: {persistedQuery: {version: 1, sha256Hash: $hash}}}'

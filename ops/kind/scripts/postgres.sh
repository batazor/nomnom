#!/usr/bin/env bash
# ADR 8: для каждого владельца PostgreSQL (orders, payments, delivery) создаёт случайный
# пароль (Secret postgres-<owner> в nomnom-data) и DSN для приложения (Secret
# <owner>-postgres в nomnom-<owner>). Существующий пароль сохраняется, чтобы повторный
# deploy не расходился с данными работающей базы.
set -euo pipefail
context="${1:?usage: postgres.sh <kube-context>}"
k() { kubectl --context "${context}" "$@"; }

for owner in orders payments delivery; do
  password="$(k -n nomnom-data get secret "postgres-${owner}" -o jsonpath='{.data.password}' 2>/dev/null | base64 -d || true)"
  [[ -n "${password}" ]] || password="$(openssl rand -hex 24)"
  k -n nomnom-data create secret generic "postgres-${owner}" --from-literal=password="${password}" \
    --dry-run=client -o yaml | k apply -f - >/dev/null
  dsn="postgres://${owner}:${password}@postgres-${owner}.nomnom-data.svc.cluster.local:5432/${owner}?sslmode=disable"
  k -n "nomnom-${owner}" create secret generic "${owner}-postgres" --from-literal=dsn="${dsn}" \
    --dry-run=client -o yaml | k apply -f - >/dev/null
  echo "postgres-${owner} credentials published"
done

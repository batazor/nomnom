#!/usr/bin/env bash
# ADR 8: создаёт случайный пароль PostgreSQL orders (Secret postgres-orders в nomnom-data)
# и DSN для приложения (Secret orders-postgres в nomnom-orders). Существующий пароль
# сохраняется, чтобы повторный deploy не расходился с данными работающей базы.
set -euo pipefail
context="${1:?usage: postgres.sh <kube-context>}"
k() { kubectl --context "${context}" "$@"; }

password="$(k -n nomnom-data get secret postgres-orders -o jsonpath='{.data.password}' 2>/dev/null | base64 -d || true)"
[[ -n "${password}" ]] || password="$(openssl rand -hex 24)"
k -n nomnom-data create secret generic postgres-orders --from-literal=password="${password}" \
  --dry-run=client -o yaml | k apply -f - >/dev/null
dsn="postgres://orders:${password}@postgres-orders.nomnom-data.svc.cluster.local:5432/orders?sslmode=disable"
k -n nomnom-orders create secret generic orders-postgres --from-literal=dsn="${dsn}" \
  --dry-run=client -o yaml | k apply -f - >/dev/null
echo "postgres-orders credentials published"

# Proposal

## Why

Все gRPC-серверы `orders`, `payments`, `restaurants` и `delivery` отклоняют вызовы `UNAUTHENTICATED`: verified identity не спроектирована, поэтому Order Fulfillment Workflow не может вызвать командные API соседей. [ADR 27](../../../docs/adr/0027-service-identity-via-projected-serviceaccount-tokens.md) выбирает механизм для service-to-service вызовов.

## What Changes

- ADR 27: service identity — Kubernetes projected ServiceAccount token с audience на целевой контекст, проверка по JWKS кластера, allowlist ServiceAccount → `service` subject.
- `ServiceTokenSubjectResolver` во всех четырёх контекстах с одинаковой конфигурацией `IDENTITY__*`; `RejectingSubjectResolver` остаётся при незаданной конфигурации.
- Клиенты `orders` прикладывают `authorization: Bearer` из `IDENTITY__TOKEN_DIR/<audience>`.
- Общие тестовые векторы `tests/identity` (JWKS и токены), которые проверяет каждый контекст.
- Helm: projected token volumes, JWKS ConfigMap и env; kind: создание ConfigMap с JWKS кластера.
- `docs/security.md`: решение для service identity; user identity через BFF остаётся открытым.

## Capabilities

### New Capabilities

- `service-identity`: аутентификация `service` identity между bounded contexts.

### Modified Capabilities

Нет.

## Impact

`infrastructure/grpc` и конфигурация четырёх контекстов, `orders` grpcclient, Helm charts, `ops/kind`, `docs/security.md`. Authorization (SpiceDB grants) не меняется: in-memory проверки permissions по-прежнему решают доступ.

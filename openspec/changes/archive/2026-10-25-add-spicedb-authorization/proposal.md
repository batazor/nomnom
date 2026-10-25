# Proposal

## Why

После [ADR 27](../../../docs/adr/0027-service-identity-via-projected-serviceaccount-tokens.md) вызовы `orders-worker` аутентифицированы, но authorization во всех контекстах выполняют in-memory заглушки, запрещающие всё. `authorization/schema.zed` из [security](../../../docs/security.md#schema-как-shared-contract) не создана, поэтому SpiceDB (ADR 18) нечем наполнить.

## What Changes

- `authorization/schema.zed` — первая общая schema; `authorization/bootstrap.yaml` — grants развёртывания (`platform:nomnom#fulfillment@service:orders-worker`) и assertions; `make authz-validate`, `make spicedb-dev`.
- Команды Order Fulfillment Workflow — capabilities на синглтоне `platform:nomnom`; пользовательские checks — resource-level permissions.
- SpiceDB-адаптеры authorization-портов в `orders` (Go, `authzed-go`), `payments` (Python, `authzed`), `restaurants` (TypeScript, `@authzed/authzed-node`), `delivery` (Rust, клиент из `buf.build/authzed/api`).
- `orders`: creation-time `TOUCH order#customer@user` в `placeorder` через порт `Relationships`.
- Конфигурация `SPICEDB__ENDPOINT`, `SPICEDB__PRESHARED_KEY`, `SPICEDB__INSECURE`, `SPICEDB__TIMEOUT`; без endpoint процесс, проверяющий права, не стартует.
- kind: SpiceDB (memory datastore) с bootstrap, egress NetworkPolicy, values контекстов.

## Capabilities

### New Capabilities

- `spicedb-authorization`: authoritative authorization bounded contexts через SpiceDB и grants `service` identities.

### Modified Capabilities

Нет.

## Impact

Authorization-адаптеры и конфигурация четырёх контекстов, `authorization/`, корневой Makefile, Helm/kind. Writers relationships для `restaurant#owner/staff`, `courier`, `delivery#courier` и reconciliation — отдельные changes; до них пользовательские checks этих ресурсов отклоняются.

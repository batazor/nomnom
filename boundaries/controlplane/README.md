# Control plane

Глобальный control plane cell-based архитектуры ([ADR 41](../../docs/adr/0041-cell-based-architecture-with-control-plane.md)):
реестр Cell, Market и Market Placement. Go, PostgreSQL, gRPC API
`nomnom.controlplane.v1.ControlPlaneService` ([proto](infrastructure/grpc/nomnom/controlplane/v1/service.proto)).
Язык — [GLOSSARY](GLOSSARY.md), локальные решения — [docs/adr](docs/adr).

Control plane не в горячем пути: ячейки к нему не обращаются, BFF держит Placement Map в памяти
(`BFF__PLACEMENT_TTL`) и вызывает `GetPlacementMap`/`ListMarkets` через internal gateway.

## API

| RPC | Кто вызывает | Правило |
| --- | --- | --- |
| `RegisterCell` | оператор (user assertion через делегата, ADR 32) | `platform#manage_markets` (SpiceDB, ADR 18); повтор с теми же значениями — `created = false`, с другими — `ALREADY_EXISTS` |
| `CreateMarket` | оператор | то же; незарегистрированная Cell — `FAILED_PRECONDITION` (`CELL_NOT_FOUND`) |
| `ListMarkets` | service identity BFF | вызывающий service — в `IDENTITY__PLACEMENT_READERS`; по name, затем market_id |
| `GetPlacementMap` | service identity BFF | то же; по market_id |

Ошибки — `google.rpc.ErrorInfo` домена `nomnom.controlplane.v1`:

| Код | Reason |
| --- | --- |
| `UNAUTHENTICATED` | `AUTHENTICATION_REQUIRED` — нет/неверный projected token, assertion не от делегата |
| `INVALID_ARGUMENT` | `INVALID_ARGUMENT` — slug, `host:port`, название, валюта |
| `PERMISSION_DENIED` | `OPERATION_NOT_ALLOWED` |
| `ALREADY_EXISTS` | `CELL_ALREADY_REGISTERED`, `MARKET_ALREADY_EXISTS` |
| `FAILED_PRECONDITION` | `CELL_NOT_FOUND` |
| `UNAVAILABLE` | `DEPENDENCY_UNAVAILABLE` — SpiceDB не ответил (fail closed) |
| `INTERNAL` | `INTERNAL_ERROR` |

## Устройство

- `slices/topology` — домены `cell` и `market` (value objects в `vo/`), use cases
  [`registercell`](slices/topology/applications/registercell/README.md),
  [`createmarket`](slices/topology/applications/createmarket/README.md),
  [`listmarkets`](slices/topology/applications/listmarkets/README.md),
  [`getplacementmap`](slices/topology/applications/getplacementmap/README.md); PostgreSQL-репозитории
  (pgx + squirrel, ADR 39) и проверка SpiceDB.
- Миграции встроены в бинарь и применяются при старте под advisory lock (ADR 38), набор `topology`.
- DI — Google Wire (ADR 5): `di/wire.go`, `make generate`.
- Runtime contract (ADR 17): бизнес-порт 8086, технический порт `metrics` 9086 с `/health/*`;
  OpenTelemetry (ADR 9) — OTLP metrics и traces, JSON-логи.

## Конфигурация

Все параметры обязательны, пустой — процесс не стартует ([config.example.yaml](config.example.yaml)):
`IDENTITY__ISSUER`, `IDENTITY__AUDIENCE` (`nomnom.controlplane`), `IDENTITY__JWKS_FILE`,
`IDENTITY__SERVICE_ACCOUNTS`, `IDENTITY__USER_ISSUER`, `IDENTITY__USER_JWKS_FILE`,
`IDENTITY__USER_DELEGATES`, `IDENTITY__PLACEMENT_READERS` (сервисы из allowlist),
`SPICEDB__ENDPOINT`, `SPICEDB__PRESHARED_KEY`, `POSTGRES__DSN`.

## Команды

`make run`, `make test` (PostgreSQL и SpiceDB в testcontainers, нужен Docker), `make lint`,
`make format`, `make format-check`, `make check`, `make generate`.

Развёртывание — Helm chart [`ops/helm`](ops/helm) (ADR 12); kind — [`ops/kind`](../../ops/kind/README.md).

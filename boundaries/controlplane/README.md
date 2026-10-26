# Control plane

Глобальный control plane cell-based архитектуры ([ADR 41](../../docs/adr/0041-cell-based-architecture-with-control-plane.md)):
реестр Cell, Market и Market Placement и маршруты публичного gateway из него. Go, PostgreSQL, gRPC API
`nomnom.controlplane.v1.ControlPlaneService` ([proto](infrastructure/grpc/nomnom/controlplane/v1/service.proto)).
Язык — [GLOSSARY](GLOSSARY.md), локальные решения — [docs/adr](docs/adr).

Control plane не в горячем пути: ни gateway, ни ячейки к нему не обращаются. После каждой записи
(`RegisterCell`, `CreateMarket`, в том числе повтора) и один раз при старте до readiness он
server-side apply применяет в неймспейс публичного gateway (`ROUTES__NAMESPACE`) полный набор
ресурсов из реестра ([publish routes](slices/topology/applications/publishroutes/README.md)):

- `HTTPRoute market-<market_id>` — `/m/<market_id>` в Service Router Cell, префикс снимается,
  `X-Nomnom-Market` ставится из маршрута; метка `nomnom.dev/route: market`;
- `ConfigMap`, `HTTPRouteFilter` (`directResponse`) и `HTTPRoute markets` — Envoy сам отвечает на
  `GET /markets` списком `{"markets":[{"id","name","currency"}]}` по названию.

Чтение реестра и применение — под advisory lock PostgreSQL в одной read-only транзакции, так что
параллельные записи не применят более старый снимок. Не применилось при записи — `UNAVAILABLE`
(`ROUTES_UNAVAILABLE`), запись остаётся, повтор команды применяет снова; при старте — процесс не
стартует.

## API

| RPC | Кто вызывает | Правило |
| --- | --- | --- |
| `RegisterCell` | оператор (user assertion через делегата, ADR 32) | `platform#manage_markets` (SpiceDB, ADR 18); Cell — `cell_id` и `router` (Service Cosmo Router: DNS-1123 namespace и name, порт 1..65535); повтор с теми же значениями — `created = false`, с другими — `ALREADY_EXISTS`; затем публикация маршрутов |
| `CreateMarket` | оператор | то же; незарегистрированная Cell — `FAILED_PRECONDITION` (`CELL_NOT_FOUND`); затем публикация маршрутов |
| `ListMarkets` | оператор | `platform#manage_markets`; по name, затем market_id |

Ошибки — `google.rpc.ErrorInfo` домена `nomnom.controlplane.v1`:

| Код | Reason |
| --- | --- |
| `UNAUTHENTICATED` | `AUTHENTICATION_REQUIRED` — нет/неверный projected token, assertion не от делегата |
| `INVALID_ARGUMENT` | `INVALID_ARGUMENT` — slug, Router Cell, название, валюта |
| `PERMISSION_DENIED` | `OPERATION_NOT_ALLOWED` |
| `ALREADY_EXISTS` | `CELL_ALREADY_REGISTERED`, `MARKET_ALREADY_EXISTS` |
| `FAILED_PRECONDITION` | `CELL_NOT_FOUND` |
| `UNAVAILABLE` | `DEPENDENCY_UNAVAILABLE` — SpiceDB не ответил (fail closed); `ROUTES_UNAVAILABLE` — маршруты публичного gateway не применены, повторите команду |
| `INTERNAL` | `INTERNAL_ERROR` |

## Устройство

- `slices/topology` — домены `cell` и `market` (value objects в `vo/`), use cases
  [`registercell`](slices/topology/applications/registercell/README.md),
  [`createmarket`](slices/topology/applications/createmarket/README.md),
  [`listmarkets`](slices/topology/applications/listmarkets/README.md),
  [`publishroutes`](slices/topology/applications/publishroutes/README.md); PostgreSQL-репозитории
  (pgx + squirrel, ADR 39), проверка SpiceDB и адаптер маршрутов `infrastructure/gatewayroutes`
  (client-go, dynamic client, in-cluster ServiceAccount).
- Миграции встроены в бинарь и применяются при старте под advisory lock (ADR 38), набор `topology`.
- DI — Google Wire (ADR 5): `di/wire.go`, `make generate`.
- Runtime contract (ADR 17): бизнес-порт 8086, технический порт `metrics` 9086 с `/health/*`;
  OpenTelemetry (ADR 9) — OTLP metrics и traces, JSON-логи.

## Конфигурация

Все параметры обязательны, пустой — процесс не стартует ([config.example.yaml](config.example.yaml)):
`IDENTITY__ISSUER`, `IDENTITY__AUDIENCE` (`nomnom.controlplane`), `IDENTITY__JWKS_FILE`,
`IDENTITY__SERVICE_ACCOUNTS`, `IDENTITY__USER_ISSUER`, `IDENTITY__USER_JWKS_FILE`,
`IDENTITY__USER_DELEGATES`, `SPICEDB__ENDPOINT`, `SPICEDB__PRESHARED_KEY`, `POSTGRES__DSN`,
`ROUTES__NAMESPACE`, `ROUTES__GATEWAY`, `ROUTES__LISTENER` (неймспейс маршрутов, Gateway и
listener публичного API). Kubernetes API — только in-cluster config ServiceAccount: вне кластера
процесс не стартует (`make run` — только с поднятым в кластере API).

## Команды

`make run`, `make test` (PostgreSQL и SpiceDB в testcontainers, нужен Docker), `make lint`,
`make format`, `make format-check`, `make check`, `make generate`.

Развёртывание — Helm chart [`ops/helm`](ops/helm) (ADR 12); kind — [`ops/kind`](../../ops/kind/README.md).

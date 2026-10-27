# Proposal

## Why

Сейчас router ячеек — BFF ([ADR 41](../../../docs/adr/0041-cell-based-architecture-with-control-plane.md) до правки): клиент шлёт `X-Nomnom-Market`, `cellrouter` BFF по Placement Map control plane выбирает internal gateway Cell. BFF и Cosmo Router при этом общие для всех Cell: плохой деплой BFF или Router задевает все Market, а подписки читают Kafka одной Cell. ADR 41 теперь требует тонкий router на публичном gateway и Cosmo Router с BFF внутри каждой Cell.

## What Changes

- **Публичный gateway — router ячеек.** Новый listener `api` публичного gateway. Путь `/m/{market_id}/…` ведёт в Cosmo Router Cell, где размещён Market; маршрут снимает префикс и сам выставляет `X-Nomnom-Market`. Неизвестный Market — `404` от gateway.
- **Control plane пишет маршруты.** `RegisterCell` регистрирует Service Cosmo Router Cell вместо адреса internal gateway. После `RegisterCell` и `CreateMarket`, а также при старте control plane применяет server-side apply `HTTPRoute` каждого Market и список Market (`ConfigMap` и `HTTPRouteFilter` `directResponse`), который Envoy отдаёт по `GET /markets`. **BREAKING:** `GetPlacementMap`, `Placement` и `Cell.gateway_address` удаляются, `ListMarkets` — только для оператора.
- **BFF работает внутри одной Cell.** `cellrouter` и Placement Map удаляются; слайсы вызывают internal gateway своей Cell; Market по-прежнему читается из metadata `x-nomnom-market`. **BREAKING:** GraphQL `markets` и `BFF__PLACEMENT_TTL` удаляются.
- **UI** берёт список Market из `GET /markets` и ходит в `/m/{market_id}/graphql`; заголовок `X-Nomnom-Market` больше не шлёт. `VITE_PUBLIC_GRAPHQL_URL` заменяется на `VITE_PUBLIC_API_URL`.
- **kind**: listener `api.nomnom.localhost` на публичном gateway, `ReferenceGrant` на Service BFF, права control plane в `nomnom-gateway`, сетевые политики, seed `cell-1` со Service Router, e2e маршрутизации. Неиспользуемый проброс NodePort `30080` (`BFF_HOST_PORT`) удаляется: публичный API идёт через NodePort `30085` публичного gateway. Существующий кластер не пересоздаётся — лишний проброс в нём ни к чему не подключён и исчезнет при следующем `make kind-cluster`.

Не входит: Market Migration, Previous Placement и маршруты `@{cell_id}`, вторая Cell, CI.

## Capabilities

### New Capabilities

- `cell-routing` — маршрутизация клиентских запросов по Market на публичном gateway, публикация маршрутов control plane, список Market, BFF в одной Cell.

### Modified Capabilities

Нет: маршрутизация по ячейкам в действующих specs не описана.

## Impact

proto `nomnom.controlplane.v1` (BREAKING, `make buf-breaking` против main упадёт один раз — других потребителей, кроме BFF и seed, у API нет, оба меняются в этом change), слайс `topology` control plane, миграция `cells`, новый адаптер публикации маршрутов (client-go), chart `boundaries/controlplane/ops/helm` (Role в `nomnom-gateway`, конфиг маршрутов); `boundaries/bff`: `infrastructure/cellrouter`, слайс `markets`, `src/wiring.go`, schema, `config.yaml`, chart; `boundaries/ui`: market, graphql client, runtime config; `ops/kind/manifests/gateway/public-gateway.yaml`, `ops/kind/manifests/netpol`, `ops/kind/values/{bff,controlplane}.yaml`, `ops/kind/demo/files/seed.json`, `ops/kind/e2e`, `ops/kind/cluster.yaml`, `Makefile`, `ops/kind/scripts/lib.sh`, [ADR 14](../../../docs/adr/0014-использовать-kind-для-тестового-окружения-kubernetes.md); README `bff`, `controlplane`, `ui`, `ops/kind`, `tests/performance`, GLOSSARY control plane.

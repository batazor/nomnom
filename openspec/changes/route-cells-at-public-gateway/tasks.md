# Tasks

## 1. Control plane

- [x] 1.1 proto `nomnom.controlplane.v1`: `ServiceRef`, `Cell.router` и `RegisterCellRequest.router` вместо `gateway_address` (`reserved`), удалить `GetPlacementMap` и `Placement`; generated-копии (`FR-629`).
- [x] 1.2 Домен `cell`: value object Router Cell (DNS-1123 label, порт); миграция `topology` — колонки `router_*`, удаление строк `markets` и `cells`; репозиторий; удалить слайс `getplacementmap` и `IDENTITY__PLACEMENT_READERS`; `ListMarkets` с `platform#manage_markets` (`FR-629`, `FR-633`, `AC-804`, `AC-805`, `AC-814`).
- [x] 1.3 Порт `RoutePublisher` и адаптер client-go: построение `HTTPRoute market-*`, `ConfigMap markets` (экранирование `%`), `HTTPRouteFilter markets`, `HTTPRoute markets`; server-side apply под advisory lock `topology-routes`; тест построения ресурсов и тест адаптера на envtest или kind API (`FR-628`, `FR-630`, `FR-632`).
- [x] 1.4 `RegisterCell` и `CreateMarket` вызывают публикацию после записи, `ROUTES_UNAVAILABLE`; публикация до readiness при старте; конфиг `ROUTES__NAMESPACE`, `ROUTES__GATEWAY`, `ROUTES__LISTENER` обязателен (`FR-630`, `FR-631`, `AC-807`, `AC-808`, `AC-810`).
- [x] 1.5 Chart `boundaries/controlplane/ops/helm`: `Role` и `RoleBinding` в неймспейсе маршрутов, env `ROUTES__*`, удалить `placementReaders`.

## 2. BFF

- [x] 2.1 Удалить `infrastructure/cellrouter` (Placements, Router), перенести `MarketFrom` и `Refusal` в `infrastructure/market`; слайсы получают соединение с internal gateway своей Cell (`FR-634`, `AC-815`, `AC-816`).
- [x] 2.2 Удалить слайс `markets`, Query `markets` и тип `Market` из schema и client schema, generated-клиент control plane, audience `nomnom.controlplane`, `BFF__PLACEMENT_TTL`; `config.yaml` и `config.production.yaml` — `X-Nomnom-Market` убрать из CORS, `propagate` оставить.
- [x] 2.3 Chart `boundaries/bff/ops/helm`: удалить `cellRouter`, добавить `ReferenceGrant` для `HTTPRoute` из неймспейса публичного gateway (values: неймспейс).

## 3. UI

- [x] 3.1 `VITE_PUBLIC_API_URL` вместо `VITE_PUBLIC_GRAPHQL_URL` (runtime config, `vite.config.ts`, `.env.example`, Faro `bffUrlPattern`); список Market через `GET /markets`; GraphQL и SSE на `/m/{market_id}/graphql` без `x-nomnom-market`; моки и тесты (`FR-635`, `AC-817`, `AC-818`).

## 4. kind

- [x] 4.1 `public-gateway.yaml`: listener `api` (`api.nomnom.localhost`), политики webhooks сузить до listener `https`, `BackendTrafficPolicy` маршрутов Market по метке без request timeout, `SecurityPolicy` CORS на `HTTPRoute markets` (`FR-628`).
- [x] 4.2 Сетевые политики: egress control plane к kube-apiserver, ingress BFF из публичного gateway; удалить egress BFF к маршрутам control plane, если он отдельный.
- [x] 4.3 `values/controlplane.yaml` (`ROUTES__*`), `values/bff.yaml` (без `cellRouter` и audience control plane), `demo/files/seed.json` и `seed.py` — `router` вместо `gatewayAddress`; GRPCRoute `GetPlacementMap` убрать из `manifests/gateway/gateway.yaml`.
- [x] 4.4 Удалить проброс NodePort `30080` и `BFF_HOST_PORT`: `ops/kind/cluster.yaml`, `Makefile`, `ops/kind/scripts/lib.sh`, `ops/kind/README.md`, ADR 14.
- [x] 4.5 e2e `ops/kind/e2e/cell-routing`: запрос через публичный gateway на `/m/berlin/graphql`, перезапись заголовка клиента, `404` неизвестного Market, `GET /markets`, восстановление удалённого маршрута после рестарта control plane, обслуживание при остановленном control plane (`AC-801`–`AC-803`, `AC-806`, `AC-809`, `AC-811`, `AC-813`); `ops/kind/e2e/controlplane` — новый `RegisterCell` и `ListMarkets` только оператору.

## 5. Документация и проверка

- [x] 5.1 README `bff`, `controlplane`, `ui`, `ops/kind` (доступ к API через публичный gateway вместо port-forward `svc/bff`), `tests/performance`; GLOSSARY control plane (Router Cell, маршрут Market вместо Placement Map).
- [x] 5.2 Проверки каждого сервиса, `openspec validate route-cells-at-public-gateway --strict`, прогон в kind.

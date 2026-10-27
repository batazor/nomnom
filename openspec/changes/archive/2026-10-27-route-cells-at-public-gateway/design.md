# Design

## Термины

- **Router Cell** — Cosmo Router с plugin BFF одной Cell; его Kubernetes Service — точка входа Cell для клиентов.
- **Маршрут Market** — `HTTPRoute` публичного gateway, который ведёт путь `/m/{market_id}` в Router Cell его Market Placement.
- **Список Market** — JSON, который Envoy отдаёт сам по `GET /markets`; его строит control plane.

## Путь запроса

```text
браузер ── https://api.nomnom.localhost/m/berlin/graphql ──▶ публичный gateway (Envoy)
            │ HTTPRoute market-berlin: PathPrefix /m/berlin
            │   URLRewrite ReplacePrefixMatch /  → /graphql
            │   RequestHeaderModifier set X-Nomnom-Market: berlin
            ▼
          Service bff (nomnom-bff, cell-1) ── Cosmo Router ── plugin BFF ── internal gateway cell-1 ── контексты
```

- Listener `api` публичного gateway: HTTPS 8443, hostname `api.nomnom.localhost`, тот же сертификат `public-gateway-tls` (cert-manager добавит dnsName), маршруты только из `nomnom-gateway`.
- `PathPrefix` в Gateway API совпадает по границе сегмента: `/m/berlin` не совпадает с `/m/berlinx`. Путь без совпадения — `404` Envoy.
- `set` перезаписывает `X-Nomnom-Market`, присланный клиентом: Cell видит только значение маршрута.
- Cosmo Router по-прежнему пробрасывает `X-Nomnom-Market` в plugin правилом `propagate`; CORS Router больше не разрешает этот заголовок — браузер его не шлёт.
- Политики webhooks (`BackendTrafficPolicy` с `requestTimeout: 15s`, `ClientTrafficPolicy` с `bufferLimit: 64Ki`) сейчас нацелены на весь Gateway `public`; они сужаются до listener `https` (`sectionName`). Маршруты Market получают свою `BackendTrafficPolicy` через `targetSelectors` по метке `nomnom.dev/route: market`: без request timeout, иначе Envoy рвёт SSE-подписки.

## Публикация маршрутов control plane

### Реестр Cell

`RegisterCell(cell_id, router)`, где `router` — `ServiceRef{namespace, name, port}` Router Cell; `namespace` и `name` — DNS-1123 label, `port` — 1..65535, иначе `INVALID_ARGUMENT`. `Cell.gateway_address` удаляется (`reserved 2`, `reserved "gateway_address"`), адрес internal gateway control plane не нужен: в него ходит только BFF своей Cell.

Миграция `topology` заменяет колонку `gateway_address` колонками `router_namespace`, `router_name`, `router_port` (все `NOT NULL`) и удаляет строки `markets` и `cells`: значения Router для существующих Cell взять неоткуда, а реестр в kind восстанавливает seed Job (`RegisterCell` и `CreateMarket` идемпотентны). Других окружений с данными control plane нет.

### Ресурсы

Control plane строит из реестра полный набор ресурсов в неймспейсе `ROUTES__NAMESPACE` и применяет его server-side apply с field manager `nomnom-controlplane`:

| Ресурс | Имя | Содержимое |
| --- | --- | --- |
| `HTTPRoute` | `market-{market_id}` | parentRef `ROUTES__GATEWAY`/`ROUTES__LISTENER`; `PathPrefix /m/{market_id}`; `URLRewrite` `ReplacePrefixMatch: /`; `RequestHeaderModifier` `set X-Nomnom-Market`; backendRef — Service Router Cell; метки `nomnom.dev/route: market`, `nomnom.dev/market`, `nomnom.dev/cell` |
| `ConfigMap` | `markets` | ключ `response.body`: `{"markets":[{"id","name","currency"}]}`, по name, затем id |
| `HTTPRouteFilter` | `markets` | `directResponse`: `200`, `application/json`, body `ValueRef` → `ConfigMap markets` |
| `HTTPRoute` | `markets` | `GET` `Exact /markets`, `ExtensionRef` → `HTTPRouteFilter markets` |

- Body `directResponse` поддерживает command operators Envoy, поэтому `%` в названии Market экранируется как `%%`.
- CORS ответа `/markets` — статическая `SecurityPolicy` на `HTTPRoute markets` в `ops/kind/manifests/gateway`: allowed origins — настройка окружения, а не реестра.
- Ссылку `HTTPRoute` из `nomnom-gateway` на Service в неймспейсе Cell разрешает `ReferenceGrant`, который ставит chart BFF (`from: HTTPRoute nomnom-gateway`, `to: Service bff`).
- Удалений нет: в API нет удаления Cell и Market.

### Когда применяется

- **После записи.** `RegisterCell` и `CreateMarket` фиксируют изменение в PostgreSQL, затем вызывают порт `RoutePublisher`. Ошибка применения — ответ оператору `UNAVAILABLE` (`ROUTES_UNAVAILABLE`); запись в БД остаётся, повтор той же команды идемпотентен и применяет ресурсы снова. Повтор без изменений (`created = false`) тоже применяет ресурсы.
- **При старте.** Control plane применяет ресурсы один раз до readiness; ошибка — процесс не стартует.
- **Порядок.** Чтение реестра и применение выполняются в одной read-only транзакции под transaction-level advisory lock PostgreSQL (`pg_advisory_xact_lock`): два параллельных `CreateMarket` не перезапишут `ConfigMap markets` более старым снимком, а lock уходит вместе с транзакцией и не остаётся на соединении пула.

Периодической сверки нет: ресурсы меняются только этими записями.

### Права и конфиг

- ServiceAccount control plane получает `Role` в `nomnom-gateway`: `get`, `create`, `patch` на `httproutes` (`gateway.networking.k8s.io`), `httproutefilters` (`gateway.envoyproxy.io`) и `configmaps`.
- Новые обязательные параметры (пустой — процесс не стартует): `ROUTES__NAMESPACE`, `ROUTES__GATEWAY`, `ROUTES__LISTENER`. Kubernetes API — in-cluster config ServiceAccount.
- `IDENTITY__PLACEMENT_READERS` удаляется: читателей-сервисов у control plane больше нет. `ListMarkets` требует `platform#manage_markets`.
- Сетевые политики: egress control plane к kube-apiserver (Cilium entity `kube-apiserver`); ingress BFF из pod публичного gateway на порт 3010.

## BFF

- `infrastructure/cellrouter` удаляется вместе с `Placements`, `Router.Conn` и счётчиком `bff.placement_map.refreshes`. Чтение и проверка Market (`MarketFrom`, `Refusal`) переезжают в `infrastructure/market` без изменения поведения: нет, повтор или неверный формат — `INVALID_ARGUMENT`.
- Слайсы получают `*grpc.ClientConn` на audience через `gateway.Dial` к `BFF__INTERNAL_GATEWAY_ADDR` — internal gateway своей Cell.
- Слайс `markets`, Query `markets`, тип `Market` client schema, generated-клиент control plane, audience `nomnom.controlplane` и `BFF__PLACEMENT_TTL` (Helm `cellRouter.placementTTL`) удаляются.

## UI

- `VITE_PUBLIC_API_URL` — origin публичного API (`https://api.nomnom.localhost:18085` в kind); `VITE_PUBLIC_GRAPHQL_URL` удаляется.
- Список Market — `fetch(${api}/markets)`; ошибка — состояние ошибки выбора Market, как сейчас.
- GraphQL и SSE — `${api}/m/${marketId}/graphql`; смена Market по-прежнему создаёт новые clients и кеши. Заголовок `x-nomnom-market` не отправляется.

## Ограничения

- `make buf-breaking` против `main` на этом change падает (удалены RPC и поля `nomnom.controlplane.v1`); потребители API — только BFF и seed, оба меняются здесь.
- Previous Placement, маршруты `@{cell_id}` и поле предыдущих Cell в списке Market появятся с Market Migration.
- Порт-форвард `svc/bff 3010` в инструкциях kind заменяется публичным gateway. Проброс NodePort `30080` и `BFF_HOST_PORT` удаляются из `cluster.yaml`, `Makefile`, `scripts/lib.sh`, README kind и ADR 14; на уже созданном кластере проброс остаётся до пересоздания и ни к чему не подключён.

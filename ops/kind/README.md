# Тестовое окружение kind

kind используется для интеграционных тестов, которым нужен Kubernetes, и для E2E: проверки
internal gateway, NetworkPolicy, kubelet probes и установки Helm charts. Unit-тесты доменной
и прикладной логики от кластера не зависят и запускаются `check` каждого контекста.
Решение — [ADR 14](../../docs/adr/0014-использовать-kind-для-тестового-окружения-kubernetes.md).

## Требования

Docker, `kind` и `skaffold` версий из [`versions.env`](versions.env), `kubectl`, Helm 3.8+ (OCI charts), `jq`.

## Команды

Из корня репозитория:

```bash
make kind-e2e       # весь цикл для CI: кластер → skaffold run → проверки → удаление
make kind-up        # single-node кластер, предзагрузка образов платформы, Cilium + Hubble, Envoy Gateway
make kind-deploy    # skaffold run: сборка образов, загрузка в kind, gateway, charts приложений, readiness
make kind-dev       # skaffold dev: пересборка и передеплой при изменении исходников
make kind-test      # проверки; NetworkPolicy применяются внутри (фаза A без них, фаза B с ними)
make kind-diagnostics
make kind-down
```

| Переменная | По умолчанию | Назначение |
| --- | --- | --- |
| `KIND_CLUSTER_NAME` | `nomnom-test` | имя кластера; контекст всегда `kind-${KIND_CLUSTER_NAME}` |
| `BFF_HOST_PORT` | `18080` | host-порт для NodePort `30080` публичного API BFF |
| `KIND_KEEP` | `0` | `1` — не удалять кластер после `make kind-e2e` |
| `PRELOAD_PLATFORM_IMAGES` | `1` | загружать образы платформы из Docker-кеша хоста вместо pull на узле |

Все команды явно используют контекст `kind-${KIND_CLUSTER_NAME}`. Отчёт проверок и
диагностика сохраняются в `ops/kind/.artifacts/`.

## Как устроено

- `scripts/cluster.sh` создаёт кластер kind из [`cluster.yaml`](cluster.yaml) (один узел,
  default CNI отключён, `extraPortMappings` для BFF) и загружает в узел образы платформы.
  Список образов берётся из `skaffold render --module platform`, образы скачиваются один раз в
  Docker хоста, поэтому повторные прогоны не ходят за ними в сеть.
- [`skaffold/skaffold.yaml`](skaffold/skaffold.yaml), модуль `platform`: Helm-релизы Cilium
  (Hubble включён) и Envoy Gateway (с Gateway API CRD); hook сверяет версию CRD с `versions.env`.
- Модуль `apps` (требует `platform`): сборка образов bounded contexts их multi-stage Dockerfile
  с тегом `inputDigest` (хеш входов сборки), загрузка в kind без registry, charts приложений
  ([ADR 12](../../docs/adr/0012-helm-charts-per-deployable-application.md)) с values из
  [`values/`](values/) и `pullPolicy: Never`, манифесты gateway и pod проверок. Skaffold status
  check ждёт readiness workloads, hook — `Accepted`/`Programmed` у GatewayClass, Gateway и
  `BackendTrafficPolicy`.
- `orders-consumer` ([ADR 28](../../docs/adr/0028-selective-use-of-watermill-cqrs.md)) — второй
  release chart `orders` с `values-consumer.yaml` и [`values/orders-consumer.yaml`](values/orders-consumer.yaml):
  тот же образ, `command: ["/orders-consumer"]`, без Service; читает Kafka и уведомляет Temporal в
  `nomnom-infra`. Kafka и Temporal применяет `before`-hook helm-деплоера и ждёт их readiness:
  consumer подключается к ним при startup. Egress разрешён `manifests/netpol/app-infrastructure.yaml`.
- `payments-relay`, `delivery-relay`, `restaurants-relay` ([ADR 31](../../docs/adr/0031-outbox-storage-polling-relay-and-clients.md)) —
  releases charts контекстов с `values-relay.yaml` и `values/<context>-relay.yaml`: тот же образ,
  своя `command`, без Service и миграций (их выполняет основной release), один экземпляр.
  Публикуют Outbox в Kafka `nomnom-infra`; topics создаются при первой публикации.
- `restaurants-consumer` ([ADR 11](../../docs/adr/0011-transactional-inbox-and-idempotent-event-processing.md),
  [ADR 28](../../docs/adr/0028-selective-use-of-watermill-cqrs.md)) — release chart `restaurants` с
  `values-consumer.yaml` и [`values/restaurants-consumer.yaml`](values/restaurants-consumer.yaml): тот же
  образ, `node dist/cmd/restaurants-consumer/main.js`, без Service и миграций (Inbox создаёт init
  container release `restaurants`, отсюда `dependsOn`). Группа `restaurants` читает
  `orders.v1.order-events` с начала и по `OrderPlaced` создаёт Restaurant Order в MongoDB; poison —
  в `orders.v1.order-events.dlq`. Egress к Kafka и MongoDB уже разрешён
  `manifests/netpol/app-infrastructure.yaml` для всех pod `nomnom-restaurants`.
- Данные и авторизация ([ADR 8](../../docs/adr/0008-storage-strategy-postgresql-and-mongodb.md),
  [ADR 18](../../docs/adr/0018-spicedb-for-authorization.md)) применяет `before`-hook helm-деплоера
  до charts: SpiceDB в `nomnom-authz`, PostgreSQL `orders`, `payments`, `delivery` и MongoDB
  `restaurants` в `nomnom-data` (`emptyDir`). Пароли и DSN создают `scripts/postgres.sh` и
  `scripts/mongodb.sh`; Secret DSN кладётся в неймспейс приложения (для `delivery` —
  `delivery-postgres`, ключ `dsn`), а init container `delivery-migrate` применяет миграции до
  старта процесса.
- [`manifests/observability/trace-capture.yaml`](manifests/observability/trace-capture.yaml) —
  приёмник traces проверки 4.2 в `nomnom-observability`: OpenTelemetry Collector с OTLP gRPC
  `:4317` и debug exporter в stdout. Traces не хранятся и не заменяют Alloy
  ([ADR 9](../../docs/adr/0009-использовать-opentelemetry-и-grafana-observability-stack.md));
  `orders` отправляет в него traces через `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` из
  [`values/orders.yaml`](values/orders.yaml), metrics по-прежнему уходят только в Alloy.
- Profile `grafana-cloud` (`make kind-deploy GRAFANA_CLOUD=1`) добавляет Alloy в
  `nomnom-observability` и OTLP метрик `orders`/`orders-consumer` в Grafana Cloud free tier;
  настройка и лимиты — [`ops/grafana-cloud`](../grafana-cloud/README.md).
- `scripts/verify.sh` выполняет проверки, `scripts/diagnostics.sh` собирает pod logs, events,
  состояние Gateway API, `cilium-dbg status` и DROPPED flows Hubble; `scripts/e2e.sh`
  связывает шаги и собирает диагностику при ошибке.

## Версии

| Что | Где |
| --- | --- |
| kind, node image (digest), Skaffold, ожидаемая версия Gateway API CRD, образ Envoy Proxy, образ проверок | [`versions.env`](versions.env) |
| charts Cilium и Envoy Gateway | [`skaffold/skaffold.yaml`](skaffold/skaffold.yaml): Skaffold не подставляет environment в `remoteChart`/`version` |

Политика — последние стабильные релизы. Kubernetes 1.37 новее диапазонов, протестированных
Cilium 1.20 и Envoy Gateway 1.9 (1.33–1.36); совместимость подтверждается `make kind-e2e`.

## Неймспейсы

| Неймспейс | Содержимое |
| --- | --- |
| `nomnom-orders`, `nomnom-restaurants`, `nomnom-delivery`, `nomnom-payments` | приложения из Helm charts; в `orders` и `restaurants` — pod проверок `net-probe` (curl) |
| `nomnom-bff` | зарезервирован: образ BFF ещё не определён (ADR 10) |
| `nomnom-gateway` | GatewayClass, Gateway, `BackendTrafficPolicy`, `EnvoyProxy`, реестр `GRPCRoute` |
| `envoy-gateway-system` | controller Envoy Gateway и Envoy Proxy, Service `internal-gateway` |
| `nomnom-outsider` | pod `net-probe` вне прикладного контура для ingress-проверок |
| `nomnom-data` | PostgreSQL `orders`, `payments`, `delivery` и MongoDB `restaurants`; данные в `emptyDir`, каждая база доступна только своему приложению |
| `nomnom-authz` | SpiceDB с bootstrap-схемой и preshared key |
| `nomnom-observability` | `trace-capture` всегда; Alloy — только с `GRAFANA_CLOUD=1` |
| `nomnom-infra` | single-node Kafka (KRaft, `apache/kafka`) и Temporal dev server для `orders-consumer` ([`manifests/messaging`](manifests/messaging/infrastructure.yaml)); данные в `emptyDir` |

## Internal gateway

Схема, маршрутизация и политики описаны в
[ADR 15](../../docs/adr/0015-использовать-internal-gateway-для-синхронных-вызовов-бизнес-api.md),
[ADR 16](../../docs/adr/0016-запрещать-обход-internal-gateway-через-cilium-и-networkpolicy.md) и
разделе [синхронного взаимодействия](../../README.md#синхронное-взаимодействие).

Адрес gateway для приложений: `INTERNAL_GATEWAY_ADDR=internal-gateway.envoy-gateway-system.svc.cluster.local:8080`.

Реестр маршрутов — `GRPCRoute` в `nomnom-gateway`. Сейчас в нём два route сервиса
`nomnom.orders.v1.OrdersService` — `orders-v1-reads` (`GetOrder`, `ListCustomerOrders`, с retries)
и `orders-v1` (остальные методы, без retries) — и `restaurants-v1-reads`
(`nomnom.restaurants.v1.RestaurantsService`: `GetRestaurant`, `GetMenu`, с retries; catalog Place Order); вызов незарегистрированного сервиса получает 404
(gRPC `UNIMPLEMENTED`). Новый бизнес-RPC публикуется так:

1. `GRPCRoute` в `nomnom-gateway` с `method.type: Exact`, полным именем сервиса и методом;
   файл добавляется в `manifests.rawYaml` модуля `apps`. Методы с автоматическими retries —
   в отдельном route с собственной `BackendTrafficPolicy` (политика route заменяет политику
   Gateway, поэтому `timeout.http.requestTimeout: 300s` повторяется).
2. `ReferenceGrant` в неймспейсе backend'а для `GRPCRoute` из `nomnom-gateway`.
3. Порт pod с именем `grpc` и label `nomnom.dev/business-api: "true"` в values окружения.

Retries включены только для `orders-v1-reads` и `restaurants-v1-reads`: не более 2 повторов на `unavailable`,
`connect-failure`, `refused-stream` и `reset-before-request`, без per-try timeout.

## Public gateway и TLS

Входящие вызовы внешних систем идут через отдельный Envoy gateway `public`
(`manifests/gateway/public-gateway.yaml`). Он публикует только `POST /webhooks/provider`
на порт `webhooks` payments и принимает только HTTPS на `webhooks.nomnom.localhost`.

Сертификат выпускает cert-manager (platform-модуль) по аннотации Gateway от локального CA
(`manifests/certs/issuers.yaml`: самоподписанный bootstrap → корень `nomnom-local-ca`) и
продлевает сам. Корень для клиентов сохраняет `scripts/local-ca.sh`.

```bash
kubectl -n envoy-gateway-system port-forward svc/public-gateway 18085:8443
curl --cacert "$(ops/kind/scripts/local-ca.sh)" --resolve webhooks.nomnom.localhost:18085:127.0.0.1 \
  https://webhooks.nomnom.localhost:18085/webhooks/provider -X POST --data '{}'
```

На кластере, созданном из текущего `cluster.yaml`, port-forward не нужен: NodePort 30085
проброшен на `127.0.0.1:${WEBHOOKS_HOST_PORT}` (18085).

## Проверки

`make kind-test` выполняет:

| # | Проверка | Статус |
| --- | --- | --- |
| A | без NetworkPolicy `orders → restaurants` и `outsider → restaurants` напрямую отвечают 200, gateway доступен | выполняется |
| 1 | вызов Orders RPC через gateway достигает backend и требует identity (`UNAUTHENTICATED`) | выполняется |
| 2 | с NetworkPolicy прямые `orders ↔ restaurants` получают silent drop (curl timeout, не DNS и не refused) при выполненных условиях: backend Available, имя резолвится, gateway доступен и Orders RPC через него проходит; Hubble фиксирует `DROPPED`; `nomnom-outsider` не достаёт ни backend, ни gateway | выполняется |
| 3 | неизвестный gRPC-сервис через gateway → HTTP 404 (`UNIMPLEMENTED`) | выполняется |
| 4 | authorization metadata доходит до backend (`PERMISSION_DENIED` от SpiceDB); случайный `traceparent` вызывающего продолжен server span `orders` (тот же trace id, parent — span вызывающего) в логах `trace-capture` | выполняется |
| 5 | deadline вызывающего соблюдается backend'ом (`context deadline exceeded` от orders, а не timeout Envoy); retries только у идемпотентных методов | deadline выполняется; при недоступном SpiceDB (Cilium deny `fixtures/deny-orders-spicedb.yaml`) `ListCustomerOrders` повторяется ровно 2 раза, `RequestOrderCancellation` — ни разу (статистика Envoy `upstream_rq_retry`); route timeout 300 s задан `BackendTrafficPolicy` |
| 6 | инфраструктурные подключения под политиками | DNS, `orders` → свой PostgreSQL, Kafka, Temporal, Kafka из других контекстов, запрет чужой БД и Temporal для не-`orders` выполняются; Alloy — только с `GRAFANA_CLOUD=1`; MongoDB `restaurants` **заблокирована**: приложение её ещё не использует |
| 6a | после применения политик pod Available, без рестартов и проваленных kubelet-проб (Envoy Proxy, CoreDNS, Cilium) | выполняется; пробы приложений выключены до health contract (ADR 12) |
| E2E | через публичный API BFF | **заблокирована**: образ BFF не определён (ADR 10); port mapping подготовлен |

### Технические подключения

| Назначение | Источник → получатель | Порт |
| --- | --- | --- |
| Kubelet probes | узел → pod | порт пробы; разрешено Cilium для host-трафика, проверяется тестом 6a |
| DNS | pod → CoreDNS | UDP/TCP 53 |
| Telemetry | приложения → Alloy | 4317 OTLP gRPC, 4318 OTLP HTTP, 4040 Pyroscope |
| Метрики приложений | Alloy → pod | порт с именем `metrics` |
| Метрики Envoy | Alloy → Envoy Proxy | 19001 |
| xDS | Envoy Proxy → controller Envoy Gateway | 18000 |

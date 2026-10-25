# Тестовое окружение kind

kind используется для интеграционных тестов, которым нужен Kubernetes, и для E2E: проверки
internal gateway, NetworkPolicy, kubelet probes и установки Helm charts. Unit-тесты доменной
и прикладной логики от кластера не зависят и запускаются `check` каждого контекста.
Решение — [ADR 14](../../docs/adr/0014-использовать-kind-для-тестового-окружения-kubernetes.md).

## Требования

Docker, `kind` и `skaffold` версий из [`versions.env`](versions.env), `kubectl`, Helm 4
(server-side apply и `--take-ownership` в bootstrap), `jq`.

## Команды

Из корня репозитория:

```bash
make kind-e2e       # весь цикл для CI: кластер → skaffold run → проверки → удаление
make kind-up        # make kind-cluster (single-node кластер из cluster.yaml) и платформа: Cilium + Hubble, Envoy Gateway, cert-manager
make kind-deploy    # skaffold run: сборка образов, загрузка в kind, gateway, charts приложений, readiness
make kind-dev       # skaffold dev: пересборка и передеплой при изменении исходников
make kind-test      # проверки; NetworkPolicy применяются внутри (фаза A без них, фаза B с ними)
make kind-diagnostics
make kind-local-ca  # корень локального CA в ops/kind/.artifacts/nomnom-local-ca.crt (выводит путь)
make kind-down
```

| Переменная | По умолчанию | Назначение |
| --- | --- | --- |
| `KIND_CLUSTER_NAME` | `nomnom-test` | имя кластера; контекст всегда `kind-${KIND_CLUSTER_NAME}` |
| `BFF_HOST_PORT` | `18080` | host-порт для NodePort `30080` будущего публичного API BFF (сейчас BFF не публикуется) |
| `WEBHOOKS_HOST_PORT` | `18085` | host-порт для NodePort `30085` public gateway |
| `BFF_FORWARD_PORT` | `18090` | локальный порт `kubectl port-forward` к BFF в проверке 7 |
| `KIND_KEEP` | `0` | `1` — не удалять кластер после `make kind-e2e` |

Все команды явно используют контекст `kind-${KIND_CLUSTER_NAME}`. Отчёт проверок и
диагностика сохраняются в `ops/kind/.artifacts/`.

## Как устроено

- `make kind-cluster` проверяет версии kind и Skaffold по `versions.env` и создаёт кластер
  `kind create cluster --config=-` из [`cluster.yaml`](cluster.yaml) (один узел, default CNI
  отключён, `extraPortMappings` для BFF и public gateway; host-порты подставляет Make).
  Существующий кластер с тем же именем переиспользуется. Образы узел скачивает сам.
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
  [ADR 18](../../docs/adr/0018-spicedb-for-authorization.md)) и identity ставит chart
  [`bootstrap`](#bootstrap) до charts приложений: SpiceDB в `nomnom-authz`, PostgreSQL `orders`,
  `payments`, `delivery` и MongoDB `restaurants` в `nomnom-data` (`emptyDir`). Secret DSN
  кладётся в неймспейс приложения (для `delivery` — `delivery-postgres`, ключ `dsn`), а init
  container `delivery-migrate` применяет миграции до старта процесса.
- [`manifests/observability/trace-capture.yaml`](manifests/observability/trace-capture.yaml) —
  приёмник traces проверки 4.2 в `nomnom-observability`: OpenTelemetry Collector с OTLP gRPC
  `:4317` и debug exporter в stdout. Traces не хранятся и не заменяют Alloy
  ([ADR 9](../../docs/adr/0009-использовать-opentelemetry-и-grafana-observability-stack.md));
  `orders` отправляет в него traces через `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` из
  [`values/orders.yaml`](values/orders.yaml), metrics по-прежнему уходят только в Alloy.
- Profile `grafana-cloud` (`make kind-deploy GRAFANA_CLOUD=1`) добавляет Alloy в
  `nomnom-observability` и OTLP метрик `orders`/`orders-consumer` в Grafana Cloud free tier;
  настройка и лимиты — [`ops/grafana-cloud`](../grafana-cloud/README.md).
- BFF ([ADR 32](../../docs/adr/0032-user-identity-via-bff-signed-assertions.md)) — artifact
  `nomnom/bff` ([`boundaries/bff/Dockerfile`](../../boundaries/bff/Dockerfile): Cosmo Router с
  закреплённым SHA-256) и последний release `bff` в `nomnom-bff` с [`values/bff.yaml`](values/bff.yaml):
  ServiceAccount `bff`, контексты только через internal gateway, projected tokens `nomnom.orders` и
  `nomnom.restaurants`. Наружу BFF не опубликован; проверка обращается к нему через port-forward.
  Ключ подписи user assertions, `bff-jwks` и `idp-jwks` создаёт chart [`bootstrap`](#bootstrap).
  Values контекстов: `identity.userJwksConfigMap: bff-jwks`, `userIssuer: nomnom-bff`,
  `userDelegates: bff`; `nomnom-bff/bff` — в allowlist `orders` и `restaurants`. Фикстура
  `grpc-caller` работает под своим ServiceAccount `nomnom-bff/grpc-caller`: он допущен allowlist
  `orders`, но не делегат.
- `scripts/verify.sh` выполняет проверки, `scripts/diagnostics.sh` собирает pod logs, events,
  состояние Gateway API, `cilium-dbg status` и DROPPED flows Hubble; `scripts/e2e.sh`
  связывает шаги и собирает диагностику при ошибке.

## Версии

| Что | Где |
| --- | --- |
| kind, node image (digest), Skaffold, ожидаемая версия Gateway API CRD, Chainsaw | [`versions.env`](versions.env) |
| образы SpiceDB, PostgreSQL, MongoDB и Job JWKS (digest) | [`bootstrap/values.yaml`](bootstrap/values.yaml) |
| charts Cilium и Envoy Gateway | [`skaffold/skaffold.yaml`](skaffold/skaffold.yaml): Skaffold не подставляет environment в `remoteChart`/`version` |

Политика — последние стабильные релизы. Kubernetes 1.37 новее диапазонов, протестированных
Cilium 1.20 и Envoy Gateway 1.9 (1.33–1.36); совместимость подтверждается `make kind-e2e`.

## Bootstrap

Всё, что charts приложений читают при старте, описывает chart [`bootstrap`](bootstrap)
(`kind-bootstrap`, release `bootstrap` в `nomnom-data`) — решение
[ADR 34](../../docs/adr/0034-declarative-kind-bootstrap.md). Skaffold применяет `rawYaml` и свои
releases с pod template label `skaffold.dev/run-id`, а хранилища держат данные в памяти и
`emptyDir`, поэтому chart ставит не Skaffold, а одна команда `helm upgrade --install --wait`
в `before`-hook helm-деплоера после `kubectl apply` неймспейсов. Файлы вне chart передаются
`--set-file`.

| Ресурс | Как создаётся | Потребители |
| --- | --- | --- |
| ClusterIssuer `nomnom-selfsigned-bootstrap`, `nomnom-local-ca`, Certificate `cert-manager/nomnom-local-ca` | cert-manager | сертификат public gateway |
| Secret `nomnom-bff/bff-assertion-key` (`tls.key`, `tls.crt`) | Certificate cert-manager: ECDSA P-256, PKCS#8, `rotationPolicy: Never`, kid `nomnom-bff-kind` | BFF (`identity.assertionKeySecret.key: tls.key`), проверка 7 |
| ConfigMap `cluster-jwks`, `bff-jwks` (`jwks.json`) в неймспейсах контекстов | Job `identity-jwks` (hook): `/openid/v1/jwks` с проверкой issuer и публичный ключ BFF из `tls.crt`, server-side apply | `identity.jwksConfigMap`, `identity.userJwksConfigMap` контекстов |
| ConfigMap `nomnom-bff/idp-jwks` | `--set-file` из `tests/identity/fixtures/idp-jwks.json`; **только для kind** — проверка подписывает access tokens ключом `tests/identity/fixtures/idp-signing-key.pem` (`scripts/mint-jwt.py`) | BFF |
| ConfigMap `spicedb-bootstrap`, Deployment и Service `spicedb` (`nomnom-authz`) | `--set-file` из `authorization/`; checksum в pod template перезапускает SpiceDB только при изменении schema/bootstrap | — |
| Secret `spicedb-preshared-key` (`preshared-key`) в `nomnom-authz` и неймспейсах контекстов | `lookup` или `randAlphaNum 64` | SpiceDB, контексты |
| Secret `nomnom-data/postgres-<owner>` (`password`), `nomnom-<owner>/<owner>-postgres` (`dsn`), `nomnom-observability/alloy-postgres` | `lookup` или `randAlphaNum 48`; DSN из того же значения | PostgreSQL, приложения, Alloy |
| Secret `nomnom-data/mongodb-restaurants` (`root-password`, `password`, `keyfile`), `nomnom-restaurants/restaurants-mongodb` (`uri`) | `lookup` или `randAlphaNum` | MongoDB, restaurants |
| Deployments и Services PostgreSQL `orders`, `payments`, `delivery` и MongoDB `restaurants` | шаблоны chart, `emptyDir` | — |
| replica set `rs0` и пользователь `restaurants` MongoDB | Job `mongodb-restaurants-init` (hook): `mongosh`, идемпотентно | restaurants |

`lookup` читает существующий Secret, поэтому повторный deploy и `skaffold dev` учётные данные не
меняют; `helm template` без кластера рендерит новые случайные значения. Hook Jobs выполняются
после `--wait` (хранилища и сертификаты готовы) при каждом upgrade.

Рендер без кластера:

```bash
helm template bootstrap ops/kind/bootstrap -n nomnom-data \
  --set-file authz.schema=authorization/schema.zed \
  --set-file authz.bootstrap=authorization/bootstrap.yaml \
  --set-file identity.idpJwks=tests/identity/fixtures/idp-jwks.json
```

## Неймспейсы

| Неймспейс | Содержимое |
| --- | --- |
| `nomnom-orders`, `nomnom-restaurants`, `nomnom-delivery`, `nomnom-payments` | приложения из Helm charts; в `orders` и `restaurants` — pod проверок `net-probe` (curl) |
| `nomnom-bff` | BFF (release `bff`, ServiceAccount `bff`), Secret `bff-assertion-key`, ConfigMap `idp-jwks`; pod проверок `grpc-caller` (curl, ServiceAccount `grpc-caller`) |
| `nomnom-gateway` | GatewayClass, Gateway, `BackendTrafficPolicy`, `EnvoyProxy`, реестр `GRPCRoute` |
| `envoy-gateway-system` | controller Envoy Gateway и Envoy Proxy, Service `internal-gateway` |
| `nomnom-outsider` | pod `net-probe` вне прикладного контура для ingress-проверок |
| `nomnom-data` | PostgreSQL `orders`, `payments`, `delivery` и MongoDB `restaurants`; данные в `emptyDir`, каждая база доступна только своему приложению; release `bootstrap` и его hook Jobs |
| `nomnom-authz` | SpiceDB с bootstrap-схемой и preshared key |
| `nomnom-observability` | `trace-capture` всегда; Alloy — только с `GRAFANA_CLOUD=1` |
| `nomnom-infra` | single-node Kafka (KRaft, `apache/kafka`) и Temporal dev server для `orders-consumer` ([`manifests/messaging`](manifests/messaging/infrastructure.yaml)); данные в `emptyDir` |

## Internal gateway

Схема, маршрутизация и политики описаны в
[ADR 15](../../docs/adr/0015-использовать-internal-gateway-для-синхронных-вызовов-бизнес-api.md),
[ADR 16](../../docs/adr/0016-запрещать-обход-internal-gateway-через-cilium-и-networkpolicy.md) и
разделе [синхронного взаимодействия](../../README.md#синхронное-взаимодействие).

Адрес gateway для приложений: `INTERNAL_GATEWAY_ADDR=internal-gateway.envoy-gateway-system.svc.cluster.local:8080`.

Реестр маршрутов — `GRPCRoute` в `nomnom-gateway`, по route на класс методов сервиса
([ADR 15](../../docs/adr/0015-использовать-internal-gateway-для-синхронных-вызовов-бизнес-api.md)):

| Route | Методы | Политика |
| --- | --- | --- |
| `orders-v1-reads` | `GetOrder`, `ListCustomerOrders` | `reads` |
| `orders-v1` | остальные методы `OrdersService` (совпадение по сервису) | `commands` |
| `restaurants-v1-reads` | `GetRestaurant`, `GetMenu`, `ListRestaurants` | `reads` |
| `restaurants-v1-commands` | `ReceiveOrderRequest`, `CancelRestaurantOrder` | `commands` |
| `restaurants-v1-management` | `OpenRestaurant`, `CloseRestaurant`, `CreateMenu`, `AddMenuItem`, `UpdateMenuItem`, `SetMenuItemAvailability` | `commands` |
| `delivery-v1-commands` | `CreateDelivery`, `ReplanDelivery`, `CancelDelivery` | `commands` |
| `payments-v1-commands` | `CreatePayment`, `AuthorizePayment`, `CapturePayment`, `CancelAuthorization`, `RefundPayment` | `commands` |

`reads` повторяет до 2 раз на `unavailable`, `connect-failure`, `refused-stream` и
`reset-before-request`; `commands` — только на `connect-failure`, `refused-stream` и
`reset-before-request`. Вызов незарегистрированного сервиса или метода получает 404 (gRPC
`UNIMPLEMENTED`), например команды кухни, у которых ещё нет вызывающих. Новый бизнес-RPC публикуется так:

1. Метод (`method.type: Exact`, полное имя сервиса и метод) — в route своего класса и
   `targetRefs` политики `reads` или `commands`; новый route добавляется в `targetRefs` нужной
   политики. Skaffold ждёт у всех route `Accepted` и `ResolvedRefs`.
2. `ReferenceGrant` в неймспейсе backend'а для `GRPCRoute` из `nomnom-gateway`.
3. Порт pod с именем `grpc` и label `nomnom.dev/business-api: "true"` в values окружения.


## Public gateway и TLS

Входящие вызовы внешних систем идут через отдельный Envoy gateway `public`
(`manifests/gateway/public-gateway.yaml`). Он публикует только `POST /webhooks/provider`
на порт `webhooks` payments и принимает только HTTPS на `webhooks.nomnom.localhost`.

Сертификат выпускает cert-manager (platform-модуль) по аннотации Gateway от локального CA
(`bootstrap/templates/certs.yaml`: самоподписанный bootstrap → корень `nomnom-local-ca`) и
продлевает сам. Корень для клиентов сохраняет `make kind-local-ca`.

```bash
kubectl -n envoy-gateway-system port-forward svc/public-gateway 18085:8443
curl --cacert "$(make -s kind-local-ca)" --resolve webhooks.nomnom.localhost:18085:127.0.0.1 \
  https://webhooks.nomnom.localhost:18085/webhooks/provider -X POST --data '{}'
```

На кластере, созданном из текущего `cluster.yaml`, port-forward не нужен: NodePort 30085
проброшен на `127.0.0.1:${WEBHOOKS_HOST_PORT}` (18085).

## Проверки

`make kind-test` запускает `scripts/verify.sh` и тесты Chainsaw из `e2e/`
([ADR 33](../../docs/adr/0033-kind-e2e-on-kyverno-chainsaw-and-grpcurl.md)). Проверки
переносятся из `verify.sh` в Chainsaw по секциям; новые пишутся сразу в Chainsaw.

### Chainsaw (`e2e/`)

Тесты работают с уже развёрнутым окружением и вызывают бизнес-API настоящим gRPC-клиентом:
`grpcurl` с descriptor sets всех контрактов (образ `e2e/probe`, fixture `grpc-caller` в
`nomnom-bff` с projected token). Вызов описан один раз — шаблон шага
`e2e/templates/grpc-call.yaml` (bindings `method`, `authorize`, `code`, `message`, `request`);
наборы методов задаются `scenarios`. Отчёт JUnit — `.artifacts/chainsaw-report.xml`.

| Тест | Проверка |
| --- | --- |
| `gateway-registry` | все `GRPCRoute` `Accepted` и `ResolvedRefs` |
| `gateway-routes-orders` | `OrdersService` без identity доходит до orders: `UNAUTHENTICATED` от backend'а |
| `gateway-routes-to-backends` | 10 команд Order Fulfillment доходят до restaurants, delivery и payments: ответ backend'а, не Envoy |
| `gateway-rejects-unregistered` | незарегистрированный сервис и команда кухни без вызывающего: `UNIMPLEMENTED` от Envoy |

Один тест: `cd ops/kind/e2e && go run github.com/kyverno/chainsaw@$CHAINSAW_VERSION test --kube-context kind-nomnom-test --config .chainsaw.yaml --test-dir . --include-test-regex 'chainsaw/gateway-registry'`.

### verify.sh

| # | Проверка | Статус |
| --- | --- | --- |
| A | без NetworkPolicy `orders → restaurants` и `outsider → restaurants` напрямую отвечают 200, gateway доступен | выполняется |
| 2 | с NetworkPolicy прямые `orders ↔ restaurants` получают silent drop (curl timeout, не DNS и не refused) при выполненных условиях: backend Available, имя резолвится, gateway доступен и Orders RPC через него проходит; Hubble фиксирует `DROPPED`; `nomnom-outsider` не достаёт ни backend, ни gateway | выполняется |
| 4 | authorization metadata доходит до backend (`PERMISSION_DENIED` от SpiceDB); случайный `traceparent` вызывающего продолжен server span `orders` (тот же trace id, parent — span вызывающего) в логах `trace-capture` | выполняется |
| 5 | deadline вызывающего соблюдается backend'ом (`context deadline exceeded` от orders, а не timeout Envoy); retries только у идемпотентных методов | deadline выполняется; при недоступном SpiceDB (Cilium deny `fixtures/deny-orders-spicedb.yaml`) `ListCustomerOrders` повторяется ровно 2 раза, `RequestOrderCancellation` — ни разу (статистика Envoy `upstream_rq_retry`); route timeout 300 s задан `BackendTrafficPolicy` |
| 6 | инфраструктурные подключения под политиками | DNS, `orders` → свой PostgreSQL, Kafka, Temporal, Kafka из других контекстов, запрет чужой БД и Temporal для не-`orders` выполняются; Alloy — только с `GRAFANA_CLOUD=1`; MongoDB `restaurants` **заблокирована**: приложение её ещё не использует |
| 6a | после применения политик pod Available, без рестартов и проваленных kubelet-проб (Envoy Proxy, CoreDNS, Cilium) | выполняется; пробы приложений выключены до health contract (ADR 12) |
| 7 | BFF насквозь (ADR 32), через port-forward под политиками: BFF Available, probes `/health/live` и `/health/ready`; из `nomnom-bff` закрыты прямой `orders` и PostgreSQL; `bffInfo`; `order(id)` без `Authorization` — ошибка `Unauthenticated` (AC-576); с access token тестового provider для отсутствующего заказа — `{"data":{"order":null}}` без ошибок (AC-575), log `orders` — `user assertion accepted: delegate=bff`; тот же user assertion (ключ BFF, aud `nomnom.orders`) от `grpc-caller` — `UNAUTHENTICATED`, log — `service is not an allowed delegate` | выполняется |

### Технические подключения

| Назначение | Источник → получатель | Порт |
| --- | --- | --- |
| Kubelet probes | узел → pod | порт пробы; разрешено Cilium для host-трафика, проверяется тестом 6a |
| Проверка 7 → BFF | `kubectl port-forward` → pod BFF | 3010; поток идёт через kubelet внутрь pod и ingress-политикой не ограничивается; egress BFF — только базовые политики неймспейса (DNS, internal gateway, telemetry) |
| DNS | pod → CoreDNS | UDP/TCP 53 |
| Telemetry | приложения → Alloy | 4317 OTLP gRPC, 4318 OTLP HTTP, 4040 Pyroscope |
| Метрики приложений | Alloy → pod | порт с именем `metrics` |
| Метрики Envoy | Alloy → Envoy Proxy | 19001 |
| xDS | Envoy Proxy → controller Envoy Gateway | 18000 |

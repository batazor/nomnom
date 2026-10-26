# Тестовое окружение kind

kind используется для интеграционных тестов, которым нужен Kubernetes, и для E2E: проверки
internal gateway, NetworkPolicy, kubelet probes и установки Helm charts. Unit-тесты доменной
и прикладной логики от кластера не зависят и запускаются `check` каждого контекста.
Решение — [ADR 14](../../docs/adr/0014-использовать-kind-для-тестового-окружения-kubernetes.md).

## Требования

Docker, `kind` и `skaffold` версий из [`versions.env`](versions.env), `kubectl`, Helm 4
(server-side apply и `--take-ownership` в bootstrap), `jq`; для проверок — Go (Chainsaw
запускается через `go run`), `python3` и `openssl` (тестовые JWT).

## Команды

Из корня репозитория:

```bash
make kind-e2e       # весь цикл для CI: кластер → skaffold run → проверки → удаление
make kind-up        # make kind-cluster (single-node кластер из cluster.yaml) и платформа: Cilium (Hubble observer), Envoy Gateway, cert-manager
make kind-deploy    # skaffold run: сборка образов, загрузка в kind, gateway, charts приложений, readiness
make kind-dev       # skaffold dev (профиль core): пересборка и передеплой при изменении исходников
make kind-test      # тесты Chainsaw; NetworkPolicy применяются внутри и остаются
make kind-policies  # применить NetworkPolicy (kubectl apply -k manifests/netpol); kind-policies-delete — удалить
make kind-diagnostics
make kind-local-ca  # корень локального CA в ops/kind/.artifacts/nomnom-local-ca.crt (выводит путь)
make kind-down
```

| Переменная | По умолчанию | Назначение |
| --- | --- | --- |
| `KIND_CLUSTER_NAME` | `nomnom-test` | имя кластера; контекст всегда `kind-${KIND_CLUSTER_NAME}` |
| `BFF_HOST_PORT` | `18080` | host-порт для NodePort `30080` будущего публичного API BFF (сейчас BFF не публикуется) |
| `WEBHOOKS_HOST_PORT` | `18085` | host-порт для NodePort `30085` public gateway |
| `KIND_STAND` | `full` для kind-deploy, `core` для kind-dev | `core` — без fixtures проверок (net-probe, grpc-caller); `make kind-test` требует `full` |
| `KIND_KEEP` | `0` | `1` — не удалять кластер после `make kind-e2e` |

Все команды явно используют контекст `kind-${KIND_CLUSTER_NAME}`. Отчёт проверок (JUnit) и
диагностика сохраняются в `ops/kind/.artifacts/`.

## Как устроено

- `make kind-cluster` проверяет версии kind и Skaffold по `versions.env` и создаёт кластер
  `kind create cluster --config=-` из [`cluster.yaml`](cluster.yaml) (один узел, default CNI
  отключён, `extraPortMappings` для BFF и public gateway; host-порты подставляет Make).
  Существующий кластер с тем же именем переиспользуется. Образы узел скачивает сам.
- [`skaffold/skaffold.yaml`](skaffold/skaffold.yaml), модуль `platform`: Helm-релизы Cilium
  (Hubble только как observer в агенте: без relay и метрик) и Envoy Gateway (с Gateway API CRD); hook
  сверяет версию CRD с `versions.env`. Releases с `upgradeOnChange: true`: без него Skaffold не
  обновляет установленный remote chart, и правка values не доходит до существующего кластера.
- Модуль `apps` (требует `platform`): сборка образов bounded contexts их multi-stage Dockerfile
  с тегом `inputDigest` (хеш входов сборки), загрузка в kind без registry, charts приложений
  ([ADR 12](../../docs/adr/0012-helm-charts-per-deployable-application.md)) с values из
  [`values/`](values/) и `pullPolicy: Never`, манифесты gateway и pod проверок. Skaffold status
  check ждёт readiness workloads, hook — `Accepted`/`Programmed` у GatewayClass, Gateway и
  `BackendTrafficPolicy`.
- `before`-hook модуля `apps` до первого workload применяет ValidatingAdmissionPolicy
  [`manifests/admission/workload-baseline.yaml`](manifests/admission/workload-baseline.yaml)
  ([ADR 17](../../docs/adr/0017-cloud-native-application-runtime.md)): в неймспейсах с
  `nomnom.dev/tier` (`app`, `infra`, `gateway`, `outsider`) API server отклоняет workload без
  memory requests/limits у любого container, с образом без tag или с `latest` и без label
  `app.kubernetes.io/name`. Ошибка видна сразу в выводе `helm`/`kubectl apply`.
- `orders-consumer` ([ADR 28](../../docs/adr/0028-selective-use-of-watermill-cqrs.md)) — второй
  release chart `orders` с `values-consumer.yaml` и [`values/orders-consumer.yaml`](values/orders-consumer.yaml):
  тот же образ, `command: ["/orders-consumer"]`, без Service; читает Kafka и уведомляет Temporal в
  `nomnom-infra`. Kafka и Temporal применяет `before`-hook helm-деплоера и ждёт их readiness:
  consumer подключается к ним при startup. Egress разрешён `manifests/netpol/app-infrastructure.yaml`.
- `orders-worker` (Temporal worker Order Fulfillment Workflow) — release chart `orders` с
  `values-worker.yaml` и [`values/orders-worker.yaml`](values/orders-worker.yaml): тот же образ,
  своя `command`, без Service, `dependsOn: [orders]`. Worker опрашивает Temporal в `nomnom-infra` (Search Attribute стадии
  создаёт сам: `TEMPORAL__REGISTER_SEARCH_ATTRIBUTES=true`, dev server держит namespace в памяти) и
  вызывает restaurants, delivery и payments через internal gateway с projected tokens их audiences;
  ServiceAccount `nomnom-orders/orders-worker` — в allowlist всех трёх. Egress к PostgreSQL, Kafka и
  Temporal разрешён `manifests/netpol/app-infrastructure.yaml` для всех pod `nomnom-orders`.
- Outbox всех контекстов доставляет в Kafka Debezium в Kafka Connect `nomnom-infra`
  ([ADR 37](../../docs/adr/0037-outbox-delivery-via-debezium-cdc.md)): PostgreSQL — из WAL
  (`wal_level=logical`, publication `<context>_outbox` создают миграции), MongoDB `restaurants` —
  из change streams. Процессов relay нет. Коннектор `<context>-outbox` регистрирует hook Job
  основного release контекста (`cdc.enabled` в `values/<context>.yaml`) через REST Connect;
  пароли Connect читает из Secret `cdc-credentials` (chart [`bootstrap`](#bootstrap)).
  Connect — release `kafka-connect` (chart [`kafka-connect/chart`](kafka-connect/chart), образ
  [`kafka-connect/Dockerfile`](kafka-connect/Dockerfile): Debezium и Prometheus JMX exporter `:9404`);
  releases контекстов ставятся после него (`dependsOn`, `wait`). Метрики коннекторов и слотов
  репликации собирает Alloy (row CDC и PostgreSQL в Platform / Overview, alerts `cdc-*` и
  `replication-slot-*`). Состояние:
  `kubectl -n nomnom-infra exec deploy/kafka-connect -- curl -s localhost:8083/connectors?expand=status`.
  Topics создаются при первой публикации.
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
  `payments`, `delivery`, MongoDB `restaurants` и Valkey `restaurants` (кеш чтения каталога,
  [ADR 40](../../docs/adr/0040-application-caching-policy.md): без persistence, ACL-пользователь
  `restaurants` с ключами `restaurants:*`, URL — Secret `restaurants-valkey`; пользователь `exporter`
  для Alloy — только INFO и CONFIG GET, пароль — Secret `alloy-valkey`) в `nomnom-data` (`emptyDir`). Secret DSN
  кладётся в неймспейс приложения (для `delivery` — `delivery-postgres`, ключ `dsn`). Миграции
  каждый процесс применяет сам при старте ([ADR 38](../../docs/adr/0038-schema-migrations-at-process-startup.md)).

- Profile `grafana-cloud` (`make kind-deploy GRAFANA_CLOUD=1`) добавляет Alloy в
  `nomnom-observability` и OTLP метрик `orders`/`orders-consumer` в Grafana Cloud free tier;
  настройка и лимиты — [`ops/grafana-cloud`](../grafana-cloud/README.md).
- BFF ([ADR 32](../../docs/adr/0032-user-identity-via-bff-signed-assertions.md)) — artifact
  `nomnom/bff` ([`boundaries/bff/Dockerfile`](../../boundaries/bff/Dockerfile): Cosmo Router с
  закреплённым SHA-256) и последний release `bff` в `nomnom-bff` с [`values/bff.yaml`](values/bff.yaml):
  ServiceAccount `bff`, контексты только через internal gateway, projected tokens `nomnom.orders` и
  `nomnom.restaurants`. Наружу BFF не опубликован; проверка `e2e/bff` на время прогона открывает
  ingress к нему из `nomnom-outsider` (`manifests/fixtures/allow-bff-from-outsider.yaml`).
  Ключ подписи user assertions, `bff-jwks`, Dex и `test-idp` создаёт chart [`bootstrap`](#bootstrap).
  Values контекстов: `identity.userJwksConfigMap: bff-jwks`, `userIssuer: nomnom-bff`,
  `userDelegates: bff`; `nomnom-bff/bff` — в allowlist `orders` и `restaurants`. **Только для
  kind**: реального identity provider нет, проверка подписывает access tokens ключом
  `tests/identity/fixtures/idp-signing-key.pem` (`scripts/mint-jwt.py`). Фикстура `grpc-caller`
  работает под своим ServiceAccount `nomnom-bff/grpc-caller`: он допущен allowlist `orders`,
  но не делегат.
- SpiceDB kind получает общий `authorization/bootstrap.yaml` и kind-only grants
  [`authz/bootstrap-kind.yaml`](authz/bootstrap-kind.yaml) (`platform:nomnom#operator@user:kind-operator`
  — оператор e2e пути заказа): chart [`bootstrap`](#bootstrap) получает его `--set-file
  authz.kindBootstrap` и дописывает его relationships к relationships общего bootstrap; checksum
  перезапуска SpiceDB считается от объединённого файла. Общий файл не меняется: grant конкретного
  человека в него не входит.
- Fixture `e2e-driver` (`nomnom-bff`, ServiceAccount `e2e-driver`, образ `e2e/probe`, projected tokens
  `nomnom.restaurants` и `nomnom.delivery`) играет оператора, владельца, кухню и курьера, у которых ещё
  нет клиентов. Только kind-values `restaurants` и `delivery` добавляют `nomnom-bff/e2e-driver=e2e-driver`
  в allowlist и `e2e-driver` в `userDelegates`; chart-default не меняется. Egress — базовые политики
  неймспейса (DNS, internal gateway) и Service `bff` (`manifests/netpol/fixtures.yaml` в kustomization
  политик, порт 3010); BFF принимает от него GraphQL.
- NetworkPolicy — kustomization [`manifests/netpol`](manifests/netpol/kustomization.yaml):
  базовые политики `app-baseline/` без namespace, overlays `namespaces/<неймспейс>` для каждого
  неймспейса приложения, политики инфраструктуры и gateways.
- Проверки — тесты Chainsaw в [`e2e/`](e2e/) (раздел «Проверки»); `scripts/diagnostics.sh`
  собирает pod logs, events, состояние Gateway API, `cilium-dbg status` и DROPPED flows Hubble;
  `scripts/e2e.sh` связывает шаги и собирает диагностику при ошибке.

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
| Deployment и Service `nomnom-data/test-idp` | ConfigMap `test-idp` из `--set-file tests/identity/fixtures/idp-jwks.json`, раздаётся busybox httpd на `:8080/jwks.json`; **только для kind** — проверки подписывают access tokens ключом `tests/identity/fixtures/idp-signing-key.pem` (`scripts/mint-jwt.py`) | BFF, Router |
| Deployment, Service и PVC `nomnom-data/dex` ([ADR 35](../../docs/adr/0035-browser-login-via-dex-and-pkce.md)) | `templates/dex.yaml`, `dex.*` в values: storage sqlite3 на PVC `dex-data` (ключи подписи и refresh tokens переживают перезапуск pod), staticPasswords (`demo` и `operator` `@nomnom.test`, пароль `nomnom`), public client `nomnom-ui`, audience `nomnom-bff`; **только для kind** | браузер (port-forward), BFF и Router (JWKS) |
| ConfigMap `spicedb-bootstrap`, Deployment и Service `spicedb` (`nomnom-authz`) | `--set-file` из `authorization/` и kind-only grants `authz/bootstrap-kind.yaml`; checksum в pod template перезапускает SpiceDB только при изменении schema/bootstrap | — |
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
  --set-file authz.kindBootstrap=ops/kind/authz/bootstrap-kind.yaml \
  --set-file identity.idpJwks=tests/identity/fixtures/idp-jwks.json
```

## Неймспейсы

| Неймспейс | Содержимое |
| --- | --- |
| `nomnom-orders`, `nomnom-restaurants`, `nomnom-delivery`, `nomnom-payments` | приложения из Helm charts; pod проверок `net-probe` (образ `e2e/probe`) |
| `nomnom-bff` | BFF (release `bff`, ServiceAccount `bff`), Secret `bff-assertion-key`; pod проверок `grpc-caller` (образ `e2e/probe`, ServiceAccount `grpc-caller`) и `e2e-driver` (образ `e2e/probe`, ServiceAccount `e2e-driver`) |
| `nomnom-gateway` | GatewayClass, Gateway, `BackendTrafficPolicy`, `EnvoyProxy`, реестр `GRPCRoute` |
| `envoy-gateway-system` | controller Envoy Gateway и Envoy Proxy, Service `internal-gateway` |
| `nomnom-outsider` | pod `net-probe` вне прикладного контура: ingress-проверки и внешний клиент BFF |
| `nomnom-data` | PostgreSQL `orders`, `payments`, `delivery` и MongoDB `restaurants`; данные в `emptyDir`, каждая база доступна только своему приложению; release `bootstrap` и его hook Jobs |
| `nomnom-authz` | SpiceDB с bootstrap-схемой и preshared key |
| `nomnom-observability` | Alloy — только с `GRAFANA_CLOUD=1` |
| `nomnom-infra` | single-node Kafka API (Redpanda dev-container, Service `kafka`), Kafka Connect с Debezium для Outbox (release `kafka-connect`, Service `kafka-connect`, ADR 37) и Temporal dev server для `orders-consumer` и `orders-worker` ([`manifests/messaging`](manifests/messaging/infrastructure.yaml)); данные в `emptyDir`, состояние Connect — в топиках брокера |

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
| `restaurants-v1-reads` | `GetRestaurant`, `GetMenu`, `ListRestaurants`, `ListOwnedRestaurants` | `reads` |
| `restaurants-v1-commands` | `ReceiveOrderRequest`, `CancelRestaurantOrder` | `commands` |
| `restaurants-v1-management` | `OpenRestaurant`, `CloseRestaurant`, `CreateMenu`, `AddMenuItem`, `UpdateMenuItem`, `SetMenuItemAvailability` | `commands` |
| `restaurants-v1-kitchen` | `AcceptRestaurantOrder`, `RejectRestaurantOrder`, `StartPreparation`, `MarkRestaurantOrderAssembled`, `MarkRestaurantOrderReady`, `ReportPreparationDelay`, `ReportPreparationFailure` | `commands` |
| `restaurants-v1-kitchen-reads` | `GetKitchenQueue`, `ListIncomingOrders`, `ListActiveRestaurantOrders`, `GetCookPool` | `reads` |
| `restaurants-v1-cook-pool` | `HireCook`, `PutCookOnLine`, `TakeCookOffLine`, `RetireCook` | `commands` |
| `restaurants-v1-administration` | `RegisterRestaurant`, `AssignStaff`, `RevokeStaff` | `commands` |
| `delivery-v1-commands` | `CreateDelivery`, `ReplanDelivery`, `CancelDelivery` | `commands` |
| `delivery-v1-courier` | `UpdateCourierLocation`, `SetCourierAvailability`, `AcceptDeliveryOffer`, `RejectDeliveryOffer`, `ConfirmPickup`, `ConfirmDelivery`, `ReportDeliveryFailure` | `commands` |
| `delivery-v1-courier-reads` | `ListAvailableDeliveryOffers`, `GetCourierCurrentDelivery` | `reads` |
| `delivery-v1-administration` | `RegisterCourier` | `commands` |
| `payments-v1-commands` | `CreatePayment`, `AuthorizePayment`, `CapturePayment`, `CancelAuthorization`, `RefundPayment` | `commands` |

`reads` повторяет до 2 раз на `unavailable`, `connect-failure`, `refused-stream` и
`reset-before-request`; `commands` — только на `connect-failure`, `refused-stream` и
`reset-before-request`. Вызов незарегистрированного сервиса или метода получает 404 (gRPC
`UNIMPLEMENTED`). Новый бизнес-RPC публикуется так:

1. Метод (`method.type: Exact`, полное имя сервиса и метод) — в route своего класса и
   `targetRefs` политики `reads` или `commands`; новый route добавляется в `targetRefs` нужной
   политики. Skaffold ждёт у всех route `Accepted` и `ResolvedRefs`.
2. `ReferenceGrant` в неймспейсе backend'а для `GRPCRoute` из `nomnom-gateway`.
3. Порт pod с именем `grpc` и label `nomnom.dev/business-api: "true"` в values окружения.


## Вход в браузере (Dex)

[ADR 35](../../docs/adr/0035-browser-login-via-dex-and-pkce.md): customer UI получает access token
Dex через authorization code + PKCE. Браузер видит Dex и BFF через port-forward:

```bash
kubectl -n nomnom-data port-forward svc/dex 5556
kubectl -n nomnom-bff port-forward svc/bff 3010
```

Issuer — `http://localhost:5556/dex`; Router и BFF читают JWKS по адресу Service
(`values/bff.yaml`, `identity.providers`). `sub` пользователя Dex — закодированная пара
(userID, connector), например `CgpraW5kLW93bmVyEgVsb2NhbA` для `owner@nomnom.test`; её же
указывают как `ownerId` ресторана и subject в SpiceDB.

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

### Browser telemetry (Faro)

С profile `grafana-cloud` тот же gateway публикует на listener `telemetry.nomnom.localhost`
`POST /collect` → `faro.receiver` Alloy (`manifests/observability/faro-route.yaml`). CORS
разрешён только для `http://localhost:4173` (Vite dev server customer UI), rate limit 50 rps.
Traces браузера идут через тот же фильтр `service.namespace=nomnom`, логи и события Faro —
в Loki с `service_name=customer-ui`. Для UI:
`VITE_FARO_URL=https://telemetry.nomnom.localhost:18085/collect`; браузер должен доверять
корню из `make kind-local-ca`.

В том же profile BFF (Cosmo Router) пишет traces в Alloy по OTLP/HTTP
(`values/bff-grafana-cloud.yaml`) и продолжает `traceparent` браузера: в Tempo корневой span —
fetch `customer-ui`, под ним `query <Operation>` сервиса `bff`, вызов plugin `bff-plugin` и его
gRPC client span, `ingress`/`egress` internal gateway, затем server span и usecase контекста
(`restaurants`, `orders` с SpiceDB и SQL). Цепочка целая только после `make kind-deploy
GRAFANA_CLOUD=1`: без profile internal gateway и контексты traces не пишут, и в Tempo
остаются только spans BFF. Для проверки из браузера —
конфигурация `customer-ui-kind` в `.claude/launch.json` (BFF на `localhost:3010`, Faro на
`localhost:12347` через port-forward).

## Демо-данные

Flow board customer UI ведёт заказ на одном экране от лица одного демо-пользователя: он
покупатель, владелец ресторанов, их кухня и курьер. В UI это один вход — `demo@nomnom.test` /
`nomnom` через Dex. **Только kind**: seed не входит в charts приложений и в другие окружения.

| Роль | Что есть после deploy |
| --- | --- |
| покупатель | ничего (заказы оформляет UI) |
| владелец | три открытых ресторана с меню (EUR) |
| кухня | персонал всех трёх ресторанов |
| курьер | зарегистрирован, позиция в центре Берлина, на линии |

Subject — Dex sub пользователя `nomnom-demo-owner` (`ChFub21ub20tZGVtby1vd25lchIFbG9jYWw`): userID
остался от прежнего отдельного владельца, на него записаны рестораны в MongoDB. Курьер
`nomnom-demo-courier` из прежних deploy остаётся в PostgreSQL, но ни один вход им не владеет.

Рестораны: «Пельменная «Сибирь»», «Чайхана «Самарканд»», «Блинная «Теремок у моста»» (5–7 блюд
каждая). Данные — [`demo/files/seed.json`](demo/files/seed.json).

**Seed.** Chart [`demo`](demo) ставит Job `demo-seed-<revision>` в `nomnom-bff` на каждый
install/upgrade ([`demo/seed/seed.py`](demo/seed/seed.py), образ — стадия `demo-seed` Dockerfile `e2e/probe`: образ проверок с python3 и openssl): те же публичные gRPC-вызовы через internal gateway, что e2e пути заказа, от
ServiceAccount `demo-seed` с user assertion ключом BFF — `kind-operator` регистрирует рестораны
и курьера, владелец назначает кухню, заводит меню и открывает рестораны, курьер сообщает позицию
и выходит на линию. Seed идемпотентен: ресторан ищется по имени в `ListOwnedRestaurants`
владельца, блюдо — по имени в `GetMenu`, повторный `RegisterCourier` допускается. Relationships
SpiceDB (`owner`, `staff`, `self`) после перезапуска SpiceDB восстанавливает reconciliation
restaurants и delivery из своих хранилищ. Job — не Helm hook: internal gateway применяется после
charts, и Job ждёт его повторами; законченный Job удаляется через 10 минут.

Subjects демо-пользователей начинаются с `nomnom-`, а не с `demo-`: dispatch без геокодирования
выбирает среди свободных курьеров первого по id, и `demo-courier` забирал бы предложения курьера
e2e (`kind-e2e-courier-*`).

Вход в UI — только через Dex (`customer-ui-kind-login` в `.claude/launch.json`, порт 4180):

```bash
kubectl -n nomnom-data port-forward svc/dex 5556
kubectl -n nomnom-bff port-forward svc/bff 3010
```

## Проверки

`make kind-test` запускает тесты Kyverno Chainsaw из `e2e/`
([ADR 33](../../docs/adr/0033-kind-e2e-on-kyverno-chainsaw-and-grpcurl.md)) против уже
развёрнутого окружения; отчёт JUnit — `.artifacts/chainsaw-report.xml`, диагностика при ошибке —
`catch` тестов в выводе.

Вызовы идут из pod fixtures ([`e2e/chart/templates/probes.yaml`](e2e/chart/templates/probes.yaml),
[`e2e/chart/templates/e2e-driver.yaml`](e2e/chart/templates/e2e-driver.yaml),
образ `e2e/probe`) в нужном неймспейсе и под его NetworkPolicy: `net-probe` в неймспейсах
приложений и в `nomnom-outsider`, `grpc-caller` в `nomnom-bff` с projected token. Клиенты —
`grpcurl` с descriptor sets всех контрактов и `google/rpc`, `nc`, `nslookup`, `curl` (GraphQL).
Повторяющиеся шаги — шаблоны `e2e/templates/`:

| Шаблон | Шаг |
| --- | --- |
| `grpc-call` | gRPC-вызов (по умолчанию из `grpc-caller` через gateway): `code`, `message`, reason `ErrorInfo` |
| `tcp-connect` | TCP из pod: `open`, `closed` или `dropped` (timeout, а не DNS и не refused) |
| `dns-lookup` | имя резолвится из `net-probe` неймспейса |
| `netpol-applied` | все NetworkPolicy применены (`kubectl apply -k manifests/netpol`) и действуют |
| `driver-call` | gRPC от имени пользователя из `e2e-driver`: user assertion, выпущенный на хосте `scripts/mint-jwt.py` ключом BFF (`tls.key`); повтор до условия `until` в пределах `wait` секунд, ids — в ConfigMap состояния теста |
| `customer-graphql` | GraphQL покупателя из `e2e-driver` в Service `bff` с access token тестового identity provider; те же `until`, `wait` и состояние |

Тесты под политиками применяют NetworkPolicy сами (`netpol-applied`); после прогона политики
остаются. Тесты не повторяют то, что уже доказал деплой: Skaffold status check ждёт readiness
workloads (а `orders` и `restaurants` не стартуют без своих БД, Kafka и Temporal), hook —
`Accepted` и `ResolvedRefs` всех `GRPCRoute`.

| Тест | Проверка (прежний id `verify.sh`) |
| --- | --- |
| `gateway-routes-to-backends` | по одному методу restaurants, delivery и payments доходит через gateway до backend'а: ответ backend'а, не Envoy (1) |
| `netpol-blocks-direct-calls` | с NetworkPolicy при выполненных условиях (restaurants Available, имя резолвится, gateway доступен и отвечает `UNIMPLEMENTED` на незарегистрированный сервис, Orders RPC через него проходит) прямые `orders ↔ restaurants` получают silent drop; `nomnom-outsider` не достаёт ни backend, ни gateway; при провале `catch` выводит DROPPED flows Hubble (2.1–2.8) |
| `infra-connectivity` | под политиками Kafka открыт из delivery и payments; чужая БД и Temporal для не-`orders` закрыты |
| `workloads-healthy-under-policies` | после применения политик deployments неймспейсов приложений, `envoy-gateway-system` и `kube-system` Available, ни один контейнер не перезапускался и нет проваленных kubelet-проб (6a.1–6a.3) |
| `bff-end-to-end` | BFF Available; из `nomnom-bff` закрыты прямой `orders` и PostgreSQL; `bffInfo`; `order(id)` без `Authorization` — `Unauthenticated` (AC-576); с access token тестового provider для отсутствующего заказа — `{"data":{"order":null}}` (AC-575), то есть orders принял user assertion BFF; тот же user assertion (ключ BFF, aud `nomnom.orders`) от `grpc-caller` — `UNAUTHENTICATED`, log — `service is not an allowed delegate` (7.1, 7.3–7.7, 7.9–7.10) |
| `order-lifecycle` | путь заказа (AC-740): оператор регистрирует ресторан и курьера, владелец — персонал, меню, открытие; курьер на линии; покупатель оформляет заказ через BFF; кухня находит его в `ListIncomingOrders`, принимает и готовит, курьер принимает предложение Dispatch, забирает и доставляет; в BFF `DELIVERED` с milestones `COURIER_ASSIGNED`, `PICKED_UP`, `DELIVERED`, оплата `captured`. Ids уникальны на прогон; в конце курьер уходит с линии |

Один тест: `cd ops/kind/e2e && go run github.com/kyverno/chainsaw@$CHAINSAW_VERSION test --kube-context kind-nomnom-test --config .chainsaw.yaml --test-dir . --include-test-regex 'chainsaw/gateway-routes-to-backends'`.

### Технические подключения

| Назначение | Источник → получатель | Порт |
| --- | --- | --- |
| Kubelet probes | узел → pod | порт пробы; разрешено Cilium для host-трафика, проверяется тестом `workloads-healthy-under-policies` |
| `order-lifecycle` → BFF | `e2e-driver` → Service `bff` | 3010 HTTP; `manifests/netpol/fixtures.yaml` |
| Проверка `bff-end-to-end` → BFF | `net-probe` в `nomnom-outsider` → pod BFF | 3010; ingress открывает на время теста `fixtures/allow-bff-from-outsider.yaml`; egress BFF — только базовые политики неймспейса (DNS, internal gateway, telemetry) |
| Проверки retries → статистика Envoy | API server (proxy) → Envoy Proxy | 19001 (`/stats/prometheus`); host-трафик, политиками gateway не ограничивается |
| DNS | pod → CoreDNS | UDP/TCP 53 |
| Telemetry | приложения → Alloy | 4317 OTLP gRPC, 4318 OTLP HTTP, 4040 Pyroscope |
| Метрики приложений | Alloy → pod | порт с именем `metrics` |
| Метрики Envoy | Alloy → Envoy Proxy | 19001 |
| xDS | Envoy Proxy → controller Envoy Gateway | 18000 |

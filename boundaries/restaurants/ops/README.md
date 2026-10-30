# Ops: restaurants

Каталог `ops/` содержит операционные файлы приложения `restaurants` — всё, что нужно для его
развёртывания, но не относится к исходному коду:

```text
ops/
  helm/            # базовый Helm chart приложения
    Chart.yaml
    values.yaml
    templates/
```

Chart независим от charts других приложений и устанавливается отдельным release. Umbrella
chart сейчас не используется. Решение зафиксировано в
[ADR 12](../../../docs/adr/0012-helm-charts-per-deployable-application.md).

## Что создаёт chart

| Ресурс | Назначение |
| --- | --- |
| `Deployment` | Один контейнер с образом приложения; все изменяемые параметры в `values.yaml`. |
| `Service` (`ClusterIP`, порт `8082`) | Backend target для internal gateway и технических механизмов Kubernetes. |
| `ServiceAccount` | Отдельный аккаунт с `automountServiceAccountToken: false`. |
| `ConfigMap` | Несекретная конфигурация из `env`; создаётся только если `env` не пуст. |

Service identity ([ADR 27](../../../docs/adr/0027-service-identity-via-projected-serviceaccount-tokens.md)) задаётся
`identity.issuer`, `identity.audience` (по умолчанию `nomnom.restaurants`), `identity.serviceAccounts`
и `identity.jwksConfigMap`, user assertions BFF ([ADR 32](../../../docs/adr/0032-user-identity-via-bff-signed-assertions.md)) —
`identity.userJwksConfigMap`, `identity.userIssuer` и `identity.userDelegates`. Для release с gRPC API
(`service.enabled`) все они обязательны — иначе chart не рендерится. Ключ `jwks.json` ConfigMap
`identity.jwksConfigMap` монтируется read-only в `/etc/nomnom/identity/jwks.json`, ключ `jwks.json`
ConfigMap `identity.userJwksConfigMap` — в `/etc/nomnom/user-identity/jwks.json` (chart их не создаёт).

Authorization через SpiceDB ([ADR 18](../../../docs/adr/0018-spicedb-for-authorization.md)) задаётся `spicedb.endpoint`,
`spicedb.insecure`, `spicedb.timeout` (по умолчанию `2s`) и `spicedb.presharedKeySecret`
(`name`, `key` — по умолчанию `preshared-key`). Пока `spicedb.endpoint` пуст, переменные
`SPICEDB__*` не задаются и приложение не стартует: `SPICEDB__ENDPOINT` обязателен. Если endpoint задан,
рендерятся `SPICEDB__ENDPOINT`, `SPICEDB__INSECURE`, `SPICEDB__TIMEOUT` и
`SPICEDB__PRESHARED_KEY` из `secretKeyRef` (Secret chart не создаёт); без
`spicedb.presharedKeySecret.name` или с неверным `spicedb.timeout` chart не рендерится.
`spicedb.insecure: true` — только для kind.

Хранилище MongoDB ([ADR 8](../../../docs/adr/0008-storage-strategy-postgresql-and-mongodb.md),
[локальный ADR 2](../docs/adr/0002-mongodb-collections-and-document-boundaries.md)) задаётся
`mongodb.uriSecret` (`name`, `key` — по умолчанию `uri`), `mongodb.database` (по умолчанию
`restaurants`) и `mongodb.timeout` (по умолчанию `5s`). Нужен replica set: Unit of Work использует
multi-document транзакции. Пока `mongodb.uriSecret.name` пуст, `MONGODB__*` не задаются и
приложение не стартует: `MONGODB__URI` обязателен. Если Secret задан, рендерятся `MONGODB__URI` из
`secretKeyRef` (Secret chart не создаёт), `MONGODB__DATABASE` и `MONGODB__TIMEOUT`. Init container
миграций нет: каждый процесс при старте сам применяет новые миграции migrate-mongo — коллекции,
JSON Schema validators, индексы и миграции документов (на актуальной базе — ничего) по наборам:
`messaging` (Outbox, Inbox), затем `restaurants`, `menus`, `restaurant_orders`, `simulator`, каждый со своим changelog
`migrations_changelog_<id>` (`id` — стабильный storage id набора, не зависит от раскладки кода), под lock migrate-mongo
в коллекции `migrations_lock` (на время каждого набора); ошибка останавливает pod. Время миграции входит в бюджет `startupProbe`.
Лимиты connection pool задаёт `mongodb.pool` (`maxPoolSize` 20, `minPoolSize` 0, `maxIdleTime`
`5m`, `waitQueueTimeout` `5s`, `maxConnecting` 2) → `MONGODB__MAX_POOL_SIZE`, `MONGODB__MIN_POOL_SIZE`,
`MONGODB__MAX_IDLE_TIME`, `MONGODB__WAIT_QUEUE_TIMEOUT`, `MONGODB__MAX_CONNECTING`;
`values-consumer.yaml` задаёт `maxPoolSize` 5; миграция при старте идёт через тот же пул.
С неверным `mongodb.database`, `mongodb.timeout` или лимитом пула вне диапазона приложения chart не
рендерится.

Существующее архитектурное правило сохраняется: бизнес-вызовы между сервисами не обращаются
к `Service` напрямую и проходят через internal gateway. Ingress, `HTTPRoute`/`GRPCRoute`,
MongoDB и её Secret, Kafka, Temporal, service mesh и observability collector в chart не входят.

## Команды

Команды выполняются из каталога приложения (`boundaries/restaurants`). Chart зависит от library chart
[`bitnami/common`](https://github.com/bitnami/charts/tree/main/bitnami/common) (имена, labels,
образ, рендер values, совместимость `securityContext`); версия закреплена в `Chart.yaml` и
`Chart.lock`, а сам архив скачивается в `ops/helm/charts/` и не коммитится. Skaffold скачивает
его автоматически, при ручном запуске — сначала:

```bash
helm dependency build ops/helm
```

```bash
helm lint ops/helm
```

```bash
helm template restaurants ops/helm
```

```bash
helm upgrade --install restaurants ops/helm
```

```bash
helm uninstall restaurants
```

Из корня репозитория путь к chart — `boundaries/restaurants/ops/helm`.

### Процессы того же образа

Фоновые процессы ставятся отдельными releases того же chart с собственным values-файлом: своя
`command`, `component` и без `Service`; коллекции и индексы процесс мигрирует сам при старте,
поэтому порядок установки releases не важен. Brokers (`env.KAFKA__BROKERS`) и `mongodb.uriSecret`
задаёт окружение; без них процесс не стартует.

| Release | Values | Процесс |
| --- | --- | --- |
| `restaurants-consumer` | `values-consumer.yaml` | `delivery.v1.delivery-events` → Inbox → Record Pickup (ADR 11, ADR 28) и обновление кеша каталога (ADR 43); `OrderPlaced` не читает — Restaurant Order создаёт RPC из workflow после оплаты; реплики делят consumer group `restaurants` |

```bash
helm upgrade --install restaurants-consumer ops/helm -f ops/helm/values-consumer.yaml \
  --set env.KAFKA__BROKERS=kafka:9092 --set mongodb.uriSecret.name=restaurants-mongodb
```

Отдельного процесса публикации Outbox нет: его доставляет Debezium (см. ниже).

### CDC-коннектор Outbox

Outbox `restaurants` доставляет в Kafka коннектор Debezium `restaurants-outbox` в общем Kafka
Connect ([ADR 37](../../../docs/adr/0037-outbox-delivery-via-debezium-cdc.md)): change streams
коллекции `restaurants.outbox`, `MongoEventRouter`, топик — поле `topic`, key — `aggregateId`, value —
`payload`, Event Envelope — заголовки из полей записи. Включается `cdc.enabled` только в release
`restaurants` (API): chart создаёт ресурс Strimzi `KafkaConnector` `restaurants-outbox` в namespace
KafkaConnect; Cluster Operator регистрирует коннектор и сам перезапускает упавшие задачи (`autoRestart`).

| Параметр | Назначение |
| --- | --- |
| `cdc.enabled` | Регистрировать коннектор; по умолчанию `false`. |
| `cdc.connect.cluster`, `cdc.connect.namespace` | Ресурс `KafkaConnect`, к которому относится коннектор, например `kafka-connect` в `nomnom-infra`. |
| `cdc.schemaRegistryUrl` | Schema Registry SMT `SchemaFraming`, например `http://schema-registry.nomnom-infra.svc.cluster.local:8081`. |
| `cdc.connectionString` | `mongodb.connection.string` коннектора — ссылка config provider Connect, например `${dir:/mnt/cdc:restaurants-uri}`, не URI с паролем. |
| `cdc.connector.config` | Контракт Outbox (общий для окружений); меняется вместе с `pkg/infrastructure/repository/outbox.ts`. |

Состояние — `GET /connectors/restaurants-outbox/status`; после исправления документа, остановившего
коннектор, — `POST /connectors/restaurants-outbox/restart?includeTasks=true`.

## Образ

Runtime-образ полностью задаётся через values и не содержит реального registry по умолчанию:

| Параметр | По умолчанию | Назначение |
| --- | --- | --- |
| `image.repository` | `nomnom/restaurants` | Имя образа; registry задаётся окружением. |
| `image.registry` | `""` | Registry образа; `global.imageRegistry` имеет приоритет. |
| `image.tag` | `""` | Tag сборки. Пустое значение означает `latest` — задавайте явно. |
| `image.digest` | `""` | Digest образа; имеет приоритет над tag. |
| `image.pullPolicy` | `IfNotPresent` | Для kind позволяет использовать локально загруженный образ. |

Пример для kind с локально собранным образом (см. раздел «Контейнер» в [README](../README.md)):

```bash
kind load docker-image nomnom/restaurants:local
helm upgrade --install restaurants ops/helm --set image.tag=local
```

Если тестовое окружение не должно обращаться к registry вовсе, задайте
`--set image.pullPolicy=Never`. kind-specific значения в базовый chart не входят; при
необходимости они оформляются отдельным файлом вида `ops/helm/values-kind.yaml`.

## Конфигурация

Приложение слушает порт `8082` (переменная `SERVER__PORT`); `service.targetPort` должен
совпадать с портом процесса. Несекретная конфигурация передаётся через `env` и попадает в
`ConfigMap`.

OpenTelemetry задаётся секцией `telemetry`, а не `env`: chart сам выставляет
`OTEL_SERVICE_NAME` (`telemetry.serviceName`: `restaurants`, в `values-consumer.yaml` —
`restaurants-consumer`),
`OTEL_RESOURCE_ATTRIBUTES` (`service.instance.id` — имя pod через downward API `POD_NAME`,
`service.version` — `image.tag`, иначе digest, иначе `unknown`, плюс
`telemetry.resourceAttributes`) и, если `telemetry.endpoint` задан,
`OTEL_EXPORTER_OTLP_ENDPOINT` (traces и metrics по OTLP/gRPC). Явные переменные chart
перекрывают одноимённые из `env`; `service.*` в `resourceAttributes` chart отклоняет.

```yaml
telemetry:
  endpoint: http://alloy.nomnom-observability.svc.cluster.local:4317
  resourceAttributes:
    deployment.environment.name: local
```

Адрес Alloy задаётся окружением и не хардкодится в chart; без endpoint экспорт выключен, а
W3C trace context всё равно передаётся. Alloy sidecar не добавляется — collector
разворачивается отдельно.

Секреты (пароли, API tokens, credentials БД и Kafka, BSR tokens) не помещаются в
`values.yaml` и `ConfigMap`. Они подключаются из внешних Kubernetes `Secret`:

```yaml
envFrom:
  - secretRef:
      name: restaurants-secrets

extraEnv:
  - name: SOME_TOKEN
    valueFrom:
      secretKeyRef:
        name: restaurants-secrets
        key: token
```

Secret-ресурсы chart не создаёт.

## Security

По умолчанию контейнер запускается с `runAsNonRoot: true`, `allowPrivilegeEscalation: false`,
сбросом всех capabilities и seccomp profile `RuntimeDefault`. Runtime-образ запускается пользователем `node` (UID `1000` в официальном образе Node.js), поэтому `podSecurityContext.runAsUser`/`runAsGroup` установлены в `1000`: без числового UID kubelet не может подтвердить `runAsNonRoot` для пользователя, заданного именем.

`readOnlyRootFilesystem` выключен и доступен через values: пока не подтверждено, что Node.js runtime и будущие зависимости не пишут во временную файловую систему.

## Runtime contract

Chart реализует [runtime contract](../../../docs/cloud-native-runtime.md) ([ADR 17](../../../docs/adr/0017-cloud-native-application-runtime.md)): контейнер объявляет технический порт `metrics` (`management.port`, по умолчанию `9082`), а probes обращаются к health endpoints на нём. Порт `metrics` не входит в `Service`: kubelet probes и Alloy scrape идут напрямую в pod, имя порта задано моделью NetworkPolicy [ADR 16](../../../docs/adr/0016-запрещать-обход-internal-gateway-через-cilium-и-networkpolicy.md).

| Параметр | По умолчанию | Назначение |
| --- | --- | --- |
| `management.port` | `9082` | Номер технического порта; передаётся приложению как `MANAGEMENT__PORT`. |
| `shutdown.readinessDelaySeconds` | `5` | `SHUTDOWN__READINESS_DELAY`: сколько pod после `SIGTERM` продолжает обслуживать запросы с readiness `503`. |
| `shutdown.drainTimeoutSeconds` | `20` | `SHUTDOWN__DRAIN_TIMEOUT`: deadline на завершение начатых запросов. |
| `shutdown.telemetryFlushTimeoutSeconds` | `5` | `SHUTDOWN__TELEMETRY_FLUSH_TIMEOUT`: deadline финального экспорта traces и metrics после drain. |
| `terminationGracePeriodSeconds` | `35` | Должен быть больше суммы delay, drain и telemetry flush; иначе `helm template` завершается ошибкой. |
| `startupProbe` | `/health/startup`, каждые 2s, до 30 попыток | Пока не пройдена, liveness и readiness не проверяются. |
| `livenessProbe` | `/health/live`, каждые 10s, 3 провала | Остаётся успешной во время shutdown. |
| `readinessProbe` | `/health/ready`, каждые 2s, 1 провал | Исключает pod из endpoints сразу после `SIGTERM`, в пределах readiness delay. |

Поля probes, кроме `enabled`, передаются в Kubernetes как есть. Приложение без HTTP-стека использует `grpc: { port: <номер технического порта>, service: <startup|liveness|readiness> }` вместо `httpGet`; `grpc` probe Kubernetes принимает только числовой порт.

## Ресурсы

`resources.requests` и `resources.limits` не заданы: профиль потребления ещё не измерен.
Значения задаются environment values после нагрузочной проверки.

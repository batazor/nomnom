# Ops: orders

Каталог `ops/` содержит операционные файлы приложения `orders` — всё, что нужно для его
развёртывания, но не относится к исходному коду:

```text
ops/
  helm/            # базовый Helm chart приложения
    Chart.yaml
    values.yaml
    values-worker.yaml   # release Temporal worker
    values-consumer.yaml # release Kafka consumer
    templates/
```

Chart независим от charts других приложений и устанавливается отдельным release. Umbrella
chart сейчас не используется. Решение зафиксировано в
[ADR 12](../../../docs/adr/0012-helm-charts-per-deployable-application.md).

## Что создаёт chart

| Ресурс | Назначение |
| --- | --- |
| `Deployment` | Один контейнер с образом приложения; все изменяемые параметры в `values.yaml`. |
| `Service` (`ClusterIP`, порт `8081`) | Backend target для internal gateway и технических механизмов Kubernetes. |
| `ServiceAccount` | Отдельный аккаунт с `automountServiceAccountToken: false`. |
| `ConfigMap` | Файл конфигурации процесса `config.yaml` из `config.spec` (монтируется в `/etc/nomnom/config.yaml`). |

Существующее архитектурное правило сохраняется: бизнес-вызовы между сервисами не обращаются
к `Service` напрямую и проходят через internal gateway. Ingress, `HTTPRoute`/`GRPCRoute`,
базы данных, Kafka, Temporal, service mesh и observability collector в chart не входят.

## Команды

Команды выполняются из каталога приложения (`boundaries/orders`). Chart зависит от library chart
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
helm template orders ops/helm
```

```bash
helm upgrade --install orders ops/helm
```

```bash
helm uninstall orders
```

Из корня репозитория путь к chart — `boundaries/orders/ops/helm`.

## PostgreSQL и Outbox

DSN передаётся из внешнего Secret: `postgres.dsnSecret.name`/`key` (env `POSTGRES__DSN`, в файле конфигурации его нет); без него
процесс не стартует. Миграции встроены в образ наборами слайсов (id `orders` и `simulator`, у каждого своя
таблица версий `schema_migrations_<id>`; id — часть контракта схемы, от имён кода не зависит): каждый процесс при старте применяет
недостающие всех наборов (golang-migrate под одним advisory lock, ожидание блокировок DDL ограничено `lock_timeout` 5 с, незавершённая миграция повторяется) и только потом запускает остальное. Пул процесса задают `config.spec.postgres.maxConns`,
`minConns`, `maxConnLifetime`, `maxConnLifetimeJitter`, `maxConnIdleTime`, `connectTimeout`,
`healthCheckPeriod`; values каждой роли задают свой `maxConns` (API 10,
worker и consumer 5), миграции при старте — 2 отдельных соединения (advisory lock и сама миграция). Пулер перед
базой — только session mode, Σ(replicas × maxConns) ≤ `pool_size`
([README](../README.md#пулер-postgresql)).

Outbox `orders_outbox` доставляет в Kafka Debezium из WAL
([ADR 37](../../../docs/adr/0037-outbox-delivery-via-debezium-cdc.md)): процесса публикации нет.
Базе нужен `wal_level = logical`, publication `orders_outbox` создаёт миграция. Коннектор
`orders-outbox` — ресурс Strimzi `KafkaConnector` release с `cdc.enabled: true` (API): `cdc.connect.*` —
ресурс `KafkaConnect`, `cdc.database.*` — адрес базы и ссылка на пароль в config provider Connect.

## Temporal worker

Worker `cmd/orders-worker` разворачивается отдельным release того же chart с `values-worker.yaml`:
`component: worker`, `command: ["/orders-worker"]`, `service.enabled: false` — без Service и бизнес-порта,
только технический порт `metrics` и probes.

Release worker описывает не `Deployment`, а `WorkerDeployment` и `Connection` [Temporal Worker
Controller](https://github.com/temporalio/temporal-worker-controller)
([ADR 5 контекста](../docs/adr/0005-order-fulfillment-orchestration.md#versioning-workflow-code)); контроллер и его
CRD должны быть установлены в кластере. Он создаёт `Deployment` на каждый Build ID (хеш pod template), передаёт
поду `TEMPORAL_DEPLOYMENT_NAME` и `TEMPORAL_WORKER_BUILD_ID` и делает версию текущей; Order Fulfillment
Workflow — `PINNED`, начатые заказы доживают на своей версии. Pod template общий с API и consumer
(`templates/_podtemplate.tpl`).

| Параметр | По умолчанию | Назначение |
| --- | --- | --- |
| `config.spec.temporal.hostPort` | пусто, обязателен | Адрес Temporal Server: `Connection` и файл конфигурации worker'а |
| `config.spec.temporal.namespace` | пусто, обязателен | Temporal namespace: `WorkerDeployment` и файл конфигурации worker'а |
| `workerDeployment.rollout` | `strategy: AllAtOnce` | Как новые workflow переходят на новую версию |
| `workerDeployment.sunset` | `scaledownDelay: 1h`, `deleteDelay: 24h` | Когда Drained версия масштабируется в 0 и удаляется |

```bash
helm template orders-worker ops/helm -f ops/helm/values-worker.yaml \
  --set config.spec.temporal.hostPort=temporal:7233 --set config.spec.temporal.namespace=default
```

```bash
helm upgrade --install orders-worker ops/helm -f ops/helm/values-worker.yaml \
  --set config.spec.temporal.hostPort=temporal:7233 --set config.spec.temporal.namespace=default
```

## Образ

Runtime-образ полностью задаётся через values и не содержит реального registry по умолчанию:

| Параметр | По умолчанию | Назначение |
| --- | --- | --- |
| `image.repository` | `nomnom/orders` | Имя образа; registry задаётся окружением. |
| `image.registry` | `""` | Registry образа; `global.imageRegistry` имеет приоритет. |
| `image.tag` | `""` | Tag сборки. Пустое значение означает `latest` — задавайте явно. |
| `image.digest` | `""` | Digest образа; имеет приоритет над tag. |
| `image.pullPolicy` | `IfNotPresent` | Для kind позволяет использовать локально загруженный образ. |

Пример для kind с локально собранным образом (см. раздел «Контейнер» в [README](../README.md)):

```bash
kind load docker-image nomnom/orders:local
helm upgrade --install orders ops/helm --set image.tag=local
```

Если тестовое окружение не должно обращаться к registry вовсе, задайте
`--set image.pullPolicy=Never`. kind-specific значения в базовый chart не входят; при
необходимости они оформляются отдельным файлом вида `ops/helm/values-kind.yaml`.

## Конфигурация

Несекретная конфигурация процесса — версионированный файл, который chart рендерит в `ConfigMap`
(ключ `config.yaml`), монтирует в `/etc/nomnom/config.yaml` и передаёт процессу через `CONFIG_FILE`.
Аннотация pod `checksum/config` перезапускает pod при изменении файла: процесс читает его только
при старте.

```yaml
apiVersion: config.nomnom.dev/v1alpha1
kind: Orders            # константа chart
metadata:
  name: orders          # values role: orders, orders-worker, orders-consumer
spec: {}                # values config.spec как есть + ключи, которыми владеет chart
```

`role` обязателен: `values.yaml` задаёт `orders`, `values-worker.yaml` и `values-consumer.yaml` —
свои роли; процесс не стартует с файлом чужой роли. `config.spec` попадает в файл через `toYaml`:
ключи — camelCase (`scheduledOrder.replanLead`), длительности — строки Go (`5s`), неизвестный ключ —
ошибка старта процесса. Ключи и значения по умолчанию — [README приложения](../README.md#запуск) и
[`config.example.yaml`](../config.example.yaml). `values-<role>.yaml` переопределяют секции своей роли,
секции других ролей процесс принимает.

Chart сам дописывает только то, чем владеет; такой ключ в values — ошибка `helm template`:

| Ключ `spec` | Значение |
| --- | --- |
| `identity.jwksFile`, `identity.userJwksFile` | Пути JWKS из `identity.jwksConfigMap` и `identity.userJwksConfigMap` |
| `identity.tokenDir` | Каталог projected tokens `identity.tokenAudiences` |
| `telemetry.resourceAttributes["service.version"]` | `image.tag`, если задан |

Chart читает из `config.spec` порты (`server.port` — порт контейнера `grpc`, `management.port` —
порт `metrics`) и бюджет shutdown; `service.port` — только порт `Service`.

OpenTelemetry — `config.spec.telemetry`, `tracing`, `metrics` и `logging`, одинаковые во всех chart:
`service.name` — роль процесса, `service.instance.id` — имя pod (env `POD_NAME` из Downward API).
`OTEL_*` и `LOG__LEVEL` chart не задаёт, процесс их не читает. Экспорт включается только флагами:

```yaml
config:
  spec:
    telemetry:
      endpoint: http://alloy.observability.svc:4317
      protocol: grpc                    # или http/protobuf
      resourceAttributes:
        deployment.environment.name: local
    tracing:
      enabled: true
    metrics:
      enabled: true
      exportInterval: 60s
    logging:
      level: info
```

Адрес Alloy и окружение задаются values окружения и не хардкодятся в chart. Alloy sidecar не
добавляется — collector разворачивается отдельно. Процессы пишут JSON-логи в stdout (README
приложения, «Observability»).

На уровне chart остаются только ссылки на Secret (`postgres.dsnSecret`, `spicedb.presharedKeySecret`),
имена ConfigMap JWKS (`identity.jwksConfigMap`, `identity.userJwksConfigMap`), `identity.tokenAudiences`
и прочие параметры развёртывания. Для release с gRPC API (`service.enabled`) обязательны оба ConfigMap
JWKS и `config.spec.identity` `issuer`, `audience`, `serviceAccounts`, `userIssuer`, `userDelegates`.

Секреты (пароли, API tokens, credentials БД и Kafka, BSR tokens) не помещаются в
`values.yaml` и `ConfigMap` — в `spec` для них нет полей. Процесс читает их из env под своими именами
(`POSTGRES__DSN`, `SPICEDB__PRESHARED_KEY`); chart задаёт их из `postgres.dsnSecret` и
`spicedb.presharedKeySecret`, остальные секреты окружения подключаются из внешних Kubernetes `Secret`:

```yaml
envFrom:
  - secretRef:
      name: orders-secrets

extraEnv:
  - name: DATABASE_PASSWORD
    valueFrom:
      secretKeyRef:
        name: orders-database
        key: password
```

Secret-ресурсы chart не создаёт.

## Security

По умолчанию контейнер запускается с `runAsNonRoot: true`, `allowPrivilegeEscalation: false`,
сбросом всех capabilities и seccomp profile `RuntimeDefault`. Runtime-образ основан на distroless `static-debian12:nonroot`, поэтому `podSecurityContext.runAsUser`/`runAsGroup` установлены в известный UID `65532`: без числового UID kubelet не может подтвердить `runAsNonRoot` для пользователя, заданного именем.

`readOnlyRootFilesystem: true` включён: статический Go-бинарник в distroless-образе не пишет в файловую систему. Если приложению понадобится временный каталог, добавьте `emptyDir` через `volumes`/`volumeMounts` или отключите настройку в values окружения.

## Runtime contract

Chart реализует [runtime contract](../../../docs/cloud-native-runtime.md) ([ADR 17](../../../docs/adr/0017-cloud-native-application-runtime.md)): контейнер объявляет технический порт `metrics` (`config.spec.management.port`, по умолчанию `9081`), а probes обращаются к health endpoints на нём. Порт `metrics` не входит в `Service`: kubelet probes и Alloy scrape идут напрямую в pod, имя порта задано моделью NetworkPolicy [ADR 16](../../../docs/adr/0016-запрещать-обход-internal-gateway-через-cilium-и-networkpolicy.md).

| Параметр | По умолчанию | Назначение |
| --- | --- | --- |
| `config.spec.management.port` | `9081` | Номер технического порта: порт контейнера `metrics` и файл конфигурации. |
| `config.spec.shutdown.readinessDelay` | `5s` | Сколько pod после `SIGTERM` продолжает обслуживать запросы с readiness `503`. |
| `config.spec.shutdown.drainTimeout` | `20s` | Deadline на завершение начатых запросов. |
| `terminationGracePeriodSeconds` | `30` | Должен быть больше суммы delay и drain; иначе `helm template` завершается ошибкой. |
| `startupProbe` | `/health/startup`, каждые 2s, до 30 попыток | Пока не пройдена, liveness и readiness не проверяются. |
| `livenessProbe` | `/health/live`, каждые 10s, 3 провала | Остаётся успешной во время shutdown. |
| `readinessProbe` | `/health/ready`, каждые 2s, 1 провал | Исключает pod из endpoints сразу после `SIGTERM`, в пределах readiness delay. |

Поля probes, кроме `enabled`, передаются в Kubernetes как есть. Приложение без HTTP-стека использует `grpc: { port: <номер технического порта>, service: <startup|liveness|readiness> }` вместо `httpGet`; `grpc` probe Kubernetes принимает только числовой порт.

## Ресурсы

`resources.requests` и `resources.limits` не заданы: профиль потребления ещё не измерен.
Значения задаются environment values после нагрузочной проверки.

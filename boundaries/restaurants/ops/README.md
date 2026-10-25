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
и `identity.jwksConfigMap`. Пока `identity.jwksConfigMap` пуст, переменные `IDENTITY__*` не задаются и
приложение отклоняет командные RPC. Если он задан, ключ `jwks.json` этого ConfigMap (chart его не
создаёт) монтируется read-only в `/etc/nomnom/identity/jwks.json`, а `identity.issuer` и
`identity.serviceAccounts` обязательны — иначе chart не рендерится.

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
`secretKeyRef` (Secret chart не создаёт), `MONGODB__DATABASE`, `MONGODB__TIMEOUT` и — при
`mongodb.migrate.enabled` (по умолчанию) — init container `migrate` с командой
`mongodb.migrate.command` (по умолчанию `node dist/cmd/restaurants-migrate/main.js`; для dev-образа
— `["node", "cmd/restaurants-migrate/main.ts"]`). Он идемпотентно создаёт коллекции, JSON Schema
validators, индексы и применяет миграции документов до старта процесса, как `orders-migrate`.
Лимиты connection pool задаёт `mongodb.pool` (`maxPoolSize` 20, `minPoolSize` 0, `maxIdleTime`
`5m`, `waitQueueTimeout` `5s`, `maxConnecting` 2) → `MONGODB__MAX_POOL_SIZE`, `MONGODB__MIN_POOL_SIZE`,
`MONGODB__MAX_IDLE_TIME`, `MONGODB__WAIT_QUEUE_TIMEOUT`, `MONGODB__MAX_CONNECTING`; `values-relay.yaml`
задаёт `maxPoolSize` 3, `values-consumer.yaml` — 5, init container `migrate` берёт
`mongodb.migrate.maxPoolSize` (2) и остальные лимиты из `mongodb.pool`.
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
`command`, `component`, без `Service` и без init container `migrate` (коллекции создаёт release
`restaurants`, поэтому он ставится первым). Brokers (`env.KAFKA__BROKERS`) и `mongodb.uriSecret`
задаёт окружение; без них процесс не стартует.

| Release | Values | Процесс |
| --- | --- | --- |
| `restaurants-relay` | `values-relay.yaml` | Outbox → Kafka, один активный экземпляр по lease (ADR 31) |
| `restaurants-consumer` | `values-consumer.yaml` | `orders.v1.order-events` → Inbox → Restaurant Order по `OrderPlaced` (ADR 11, ADR 28); реплики делят consumer group `restaurants` |

```bash
helm upgrade --install restaurants-consumer ops/helm -f ops/helm/values-consumer.yaml \
  --set env.KAFKA__BROKERS=kafka:9092 --set mongodb.uriSecret.name=restaurants-mongodb
```

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
`ConfigMap`:

```yaml
env:
  OTEL_SERVICE_NAME: restaurants
  OTEL_EXPORTER_OTLP_ENDPOINT: http://alloy.observability.svc:4317
  OTEL_RESOURCE_ATTRIBUTES: service.namespace=nomnom,deployment.environment.name=local
```

Адрес Alloy и другие значения OpenTelemetry задаются окружением и не хардкодятся в chart;
Alloy sidecar не добавляется — collector разворачивается отдельно.

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
| `terminationGracePeriodSeconds` | `30` | Должен быть больше суммы delay и drain; иначе `helm template` завершается ошибкой. |
| `startupProbe` | `/health/startup`, каждые 2s, до 30 попыток | Пока не пройдена, liveness и readiness не проверяются. |
| `livenessProbe` | `/health/live`, каждые 10s, 3 провала | Остаётся успешной во время shutdown. |
| `readinessProbe` | `/health/ready`, каждые 2s, 1 провал | Исключает pod из endpoints сразу после `SIGTERM`, в пределах readiness delay. |

Поля probes, кроме `enabled`, передаются в Kubernetes как есть. Приложение без HTTP-стека использует `grpc: { port: <номер технического порта>, service: <startup|liveness|readiness> }` вместо `httpGet`; `grpc` probe Kubernetes принимает только числовой порт.

## Ресурсы

`resources.requests` и `resources.limits` не заданы: профиль потребления ещё не измерен.
Значения задаются environment values после нагрузочной проверки.

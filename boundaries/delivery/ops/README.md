# Ops: delivery

Каталог `ops/` содержит операционные файлы приложения `delivery` — всё, что нужно для его
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
| `Deployment` | Контейнер приложения (миграции PostgreSQL применяет сам процесс при старте); все изменяемые параметры в `values.yaml`. |
| `Service` (`ClusterIP`, порт `8083`) | Backend target для internal gateway и технических механизмов Kubernetes. |
| `ServiceAccount` | Отдельный аккаунт с `automountServiceAccountToken: false`. |
| `ConfigMap` | Файл конфигурации процесса `config.yaml` из `config.spec` (ADR 17), монтируется в `/etc/nomnom/config.yaml`. |
| `ConfigMap` + `Job` `<release>-cdc-connector` | Только при `cdc.enabled`: hook post-install/post-upgrade регистрирует коннектор Debezium Outbox в Kafka Connect. |

Существующее архитектурное правило сохраняется: бизнес-вызовы между сервисами не обращаются
к `Service` напрямую и проходят через internal gateway. Ingress, `HTTPRoute`/`GRPCRoute`,
базы данных, Kafka, Temporal, service mesh и observability collector в chart не входят.

## Команды

Команды выполняются из каталога приложения (`boundaries/delivery`). Chart зависит от library chart
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
helm template delivery ops/helm
```

```bash
helm upgrade --install delivery ops/helm
```

```bash
helm uninstall delivery
```

Из корня репозитория путь к chart — `boundaries/delivery/ops/helm`.

## Образ

Runtime-образ полностью задаётся через values и не содержит реального registry по умолчанию:

| Параметр | По умолчанию | Назначение |
| --- | --- | --- |
| `image.repository` | `nomnom/delivery` | Имя образа; registry задаётся окружением. |
| `image.registry` | `""` | Registry образа; `global.imageRegistry` имеет приоритет. |
| `image.tag` | `""` | Tag сборки. Пустое значение означает `latest` — задавайте явно. |
| `image.digest` | `""` | Digest образа; имеет приоритет над tag. |
| `image.pullPolicy` | `IfNotPresent` | Для kind позволяет использовать локально загруженный образ. |

Пример для kind с локально собранным образом (см. раздел «Контейнер» в [README](../README.md)):

```bash
kind load docker-image nomnom/delivery:local
helm upgrade --install delivery ops/helm --set image.tag=local
```

Если тестовое окружение не должно обращаться к registry вовсе, задайте
`--set image.pullPolicy=Never`. kind-specific значения в базовый chart не входят; при
необходимости они оформляются отдельным файлом вида `ops/helm/values-kind.yaml`.

## Конфигурация

Несекретная конфигурация процесса — файл `config.yaml` в `ConfigMap`
([ADR 17](../../../docs/adr/0017-cloud-native-application-runtime.md)): chart монтирует его в
`/etc/nomnom/config.yaml` (`subPath`), задаёт env `CONFIG_FILE` с этим путём и `POD_NAME` через
Downward API (`service.instance.id`). Аннотация pod `checksum/config` — sha256 файла: изменение
конфигурации перезапускает pods. Переменные `SECTION__KEY`, `OTEL_*` и `LOG__LEVEL` chart не
задаёт, процесс их не читает.

Файл собирается так:

- `apiVersion: config.nomnom.dev/v1alpha1` и `kind: Delivery` — константы chart;
- `metadata.name` — обязательное значение `role` (`values.yaml` — `delivery`,
  `values-worker.yaml` — `delivery-worker`); процесс сверяет его со своей ролью;
- `spec` — блок values `config.spec` как есть (`toYaml`): ключи camelCase, длительности —
  строки (`5s`, `2m`); неизвестный ключ завершает startup процесса;
- chart дописывает только то, чем владеет: пути смонтированных JWKS в
  `spec.identity.jwksFile` и `spec.identity.userJwksFile` (при `identity.jwksConfigMap`) и
  `service.version` из `image.tag` в `spec.telemetry.resourceAttributes`. Такой ключ в values —
  ошибка `helm template`.

```yaml
role: delivery
config:
  spec:
    temporal:
      hostPort: temporal.nomnom-infra.svc.cluster.local:7233
      namespace: default
    telemetry:
      endpoint: http://alloy.observability.svc:4317
      resourceAttributes:
        deployment.environment.name: local
    tracing:
      enabled: true
    metrics:
      enabled: true
```

Приложение слушает порт `8083` (`config.spec.server.port`, по умолчанию `8083`);
`service.targetPort` должен совпадать с ним — при заданном `config.spec.server.port` chart это
проверяет. Адрес Alloy и другие значения OpenTelemetry задаются окружением и не хардкодятся в
chart; Alloy sidecar не добавляется — collector разворачивается отдельно.

`values-worker.yaml` убирает из `config.spec` секции API (`identity`, `spicedb`, `kafka`,
`deliveryEstimate`), неполные в `values.yaml`: процесс проверяет весь `spec` kind `Delivery` и
неполную секцию не принимает.

Секреты (пароли, API tokens, credentials БД и Kafka, BSR tokens) не помещаются в
`values.yaml` и `ConfigMap`, полей для них в `spec` нет. Chart подключает `DATABASE__URI`,
`VALKEY__URL` и `SPICEDB__PRESHARED_KEY` из Secret через `secretKeyRef`; остальные — из внешних
Kubernetes `Secret`:

```yaml
envFrom:
  - secretRef:
      name: delivery-secrets

extraEnv:
  - name: DATABASE_PASSWORD
    valueFrom:
      secretKeyRef:
        name: delivery-database
        key: password
```

Secret-ресурсы chart не создаёт.

## PostgreSQL

Хранилище — PostgreSQL ([ADR 31](../../../docs/adr/0031-outbox-storage-polling-relay-and-clients.md),
[ADR 0001 контекста](../docs/adr/0001-postgresql-хранилище-delivery.md)). Контейнер получает
`DATABASE__URI` из Secret `database.uriSecret` (по умолчанию `delivery-postgres`, ключ `dsn`);
лимиты пула — `config.spec.database` (`maxConns`, `minConns`, `maxLifetime`, `idleTimeout`,
`acquireTimeout`). Secret создаёт развёртывание: в kind —
chart `ops/kind/bootstrap` вместе с базой `delivery` общего сервера `postgres`. Без Secret процесс завершается
ошибкой startup (`DATABASE__URI is required`). Значение — только PostgreSQL connection URI
(`postgresql://` или `postgres://`); libpq key=value завершает startup.

Миграции встроены в бинарь (`sqlx::migrate!`), отдельного шага нет: при старте процесс до
открытия портов применяет ожидающие версии или видит актуальную схему и продолжает. Advisory
lock сериализует старт реплик; ошибка миграции завершает процесс (pod уходит в рестарт).

## CDC Outbox

Outbox `delivery_outbox` публикует коннектор Debezium `delivery-outbox` в Kafka Connect
([ADR 37](../../../docs/adr/0037-outbox-delivery-via-debezium-cdc.md)); процесса relay нет.
При `cdc.enabled` chart создаёт ресурс Strimzi `KafkaConnector` `delivery-outbox` в namespace
KafkaConnect `cdc.connect.namespace` с label `strimzi.io/cluster: <cdc.connect.cluster>`:
Cluster Operator регистрирует коннектор и сам перезапускает упавшие задачи (`autoRestart`). Конфигурация — `cdc.connector.config` (контракт Outbox:
publication и слот `delivery_outbox`, топик из поля `topic`, ключ `aggregate_id`, заголовки
Event Envelope) поверх `cdc.database`. `cdc.database.password` — ссылка config provider
Connect (`${dir:/mnt/cdc:delivery-password}`), а не пароль: Secret `cdc-credentials`
монтирует сам Connect. Publication создаёт миграция, слот — коннектор; PostgreSQL нужен
`wal_level = logical`.

```bash
helm template delivery boundaries/delivery/ops/helm \
  --set cdc.enabled=true \
  --set cdc.connect.cluster=kafka-connect --set cdc.connect.namespace=nomnom-infra \
  --set cdc.schemaRegistryUrl=http://schema-registry.nomnom-infra.svc.cluster.local:8081 \
  --set cdc.database.hostname=postgres.nomnom-data.svc.cluster.local \
  --set cdc.database.name=delivery --set cdc.database.user=delivery \
  --set 'cdc.database.password=${dir:/mnt/cdc:delivery-password}'
```

Состояние коннектора — `GET /connectors/delivery-outbox/status`; запись, нарушающая контракт,
останавливает его до `POST /connectors/delivery-outbox/restart?includeTasks=true`.

## Service identity

Проверку projected ServiceAccount token ([ADR 27](../../../docs/adr/0027-service-identity-via-projected-serviceaccount-tokens.md))
включает `identity.jwksConfigMap`: ConfigMap с ключом `jwks.json` (создаётся развёртыванием из
`kubectl get --raw /openid/v1/jwks`) монтируется read-only в `/etc/nomnom/identity/jwks.json`, и
chart пишет этот путь в `spec.identity.jwksFile`. Issuer, audience и allowlist — ключи
`config.spec.identity.issuer`, `audience` (chart: `nomnom.delivery`) и `serviceAccounts`; для
release с gRPC API (`service.enabled`) все обязательны, пустое значение — ошибка рендера chart.

```bash
helm template delivery boundaries/delivery/ops/helm \
  --set config.spec.identity.issuer=https://kubernetes.default.svc.cluster.local \
  --set config.spec.identity.serviceAccounts=nomnom/orders-worker=orders-worker \
  --set config.spec.identity.userIssuer=nomnom-bff \
  --set config.spec.identity.userDelegates=bff \
  --set identity.jwksConfigMap=cluster-jwks --set identity.userJwksConfigMap=bff-jwks \
  --set config.spec.temporal.hostPort=temporal:7233 --set config.spec.temporal.namespace=default \
  --set valkey.urlSecret.name=delivery-valkey
```

JWKS читается при старте: после обновления ConfigMap нужен rollout restart.

User assertions BFF ([ADR 32](../../../docs/adr/0032-user-identity-via-bff-signed-assertions.md))
обязательны вместе с service identity: `identity.userJwksConfigMap`,
`config.spec.identity.userIssuer` и `config.spec.identity.userDelegates`. Ключ `jwks.json`
(публичные ключи BFF, в kind — ConfigMap `bff-jwks`) монтируется read-only в
`/etc/nomnom/user-identity/jwks.json`, путь chart пишет в `spec.identity.userJwksFile`.

## SpiceDB

Авторизацию в SpiceDB ([ADR 18](../../../docs/adr/0018-spicedb-for-authorization.md)) задают
`config.spec.spicedb.endpoint`, `insecure` (plaintext, только для kind) и `timeout` (по умолчанию
`2s`). При заданном endpoint контейнер получает `SPICEDB__PRESHARED_KEY` из Secret
`spicedb.presharedKeySecret` (`secretKeyRef`, по умолчанию ключ `preshared-key`); Secret создаётся
развёртыванием, без его имени chart не рендерится. Процесс `delivery` без endpoint не стартует:
`spec.spicedb.endpoint` обязателен.

```bash
helm template delivery boundaries/delivery/ops/helm \
  --set config.spec.spicedb.endpoint=spicedb.nomnom-authz.svc:50051 \
  --set config.spec.spicedb.insecure=true \
  --set spicedb.presharedKeySecret.name=spicedb-preshared-key
```

## Security

По умолчанию контейнер запускается с `runAsNonRoot: true`, `allowPrivilegeEscalation: false`,
сбросом всех capabilities и seccomp profile `RuntimeDefault`. Dockerfile создаёт пользователя `app` с UID/GID `10001`, поэтому `podSecurityContext.runAsUser`/`runAsGroup` установлены в `10001`: без числового UID kubelet не может подтвердить `runAsNonRoot` для пользователя, заданного именем.

`readOnlyRootFilesystem` выключен и доступен через values: пока не подтверждено, что приложение и будущий profiler не пишут во временную файловую систему.

## Runtime contract

Chart реализует [runtime contract](../../../docs/cloud-native-runtime.md) ([ADR 17](../../../docs/adr/0017-cloud-native-application-runtime.md)): контейнер объявляет технический порт `metrics` (`config.spec.management.port`, chart: `9083`), а probes обращаются к health endpoints на нём. Порт `metrics` не входит в `Service`: kubelet probes и Alloy scrape идут напрямую в pod, имя порта задано моделью NetworkPolicy [ADR 16](../../../docs/adr/0016-запрещать-обход-internal-gateway-через-cilium-и-networkpolicy.md).

| Параметр | По умолчанию | Назначение |
| --- | --- | --- |
| `config.spec.management.port` | `9083` | Номер технического порта процесса и контейнерного порта `metrics`; отличается от `service.targetPort`. |
| `config.spec.shutdown.readinessDelay` | `5s` | Сколько pod после `SIGTERM` продолжает обслуживать запросы с readiness `503`. |
| `config.spec.shutdown.drainTimeout` | `20s` | Deadline на завершение начатых запросов; положительный. |
| `terminationGracePeriodSeconds` | `30` | Должен быть больше суммы delay и drain; иначе `helm template` завершается ошибкой. |
| `startupProbe` | `/health/startup`, каждые 2s, до 30 попыток | Пока не пройдена, liveness и readiness не проверяются. |
| `livenessProbe` | `/health/live`, каждые 10s, 3 провала | Остаётся успешной во время shutdown. |
| `readinessProbe` | `/health/ready`, каждые 2s, 1 провал | Исключает pod из endpoints сразу после `SIGTERM`, в пределах readiness delay. |

Поля probes, кроме `enabled`, передаются в Kubernetes как есть. Приложение без HTTP-стека использует `grpc: { port: <номер технического порта>, service: <startup|liveness|readiness> }` вместо `httpGet`; `grpc` probe Kubernetes принимает только числовой порт.

## Ресурсы

`resources.requests` и `resources.limits` не заданы: профиль потребления ещё не измерен.
Значения задаются environment values после нагрузочной проверки.

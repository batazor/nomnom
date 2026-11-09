# Ops: payments

Каталог `ops/` содержит операционные файлы приложения `payments` — всё, что нужно для его
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
| `Service` (`ClusterIP`, порт `8084`) | Backend target для internal gateway и технических механизмов Kubernetes. |
| `ServiceAccount` | Отдельный аккаунт с `automountServiceAccountToken: false`. |
| `ConfigMap` | Файл конфигурации процесса `config.yaml` из `config.spec` (см. [Конфигурация](#конфигурация)). |
| `ConfigMap` + `Job` (hook) | Регистрация коннектора Debezium в Kafka Connect при `cdc.enabled` (см. [CDC Outbox](#cdc-outbox)). |

Существующее архитектурное правило сохраняется: бизнес-вызовы между сервисами не обращаются
к `Service` напрямую и проходят через internal gateway. Ingress, `HTTPRoute`/`GRPCRoute`,
базы данных, Kafka, Temporal, service mesh и observability collector в chart не входят.

## Команды

Команды выполняются из каталога приложения (`boundaries/payments`). Chart зависит от library chart
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
helm template payments ops/helm
```

```bash
helm upgrade --install payments ops/helm
```

```bash
helm uninstall payments
```

Из корня репозитория путь к chart — `boundaries/payments/ops/helm`.

## Образ

Runtime-образ полностью задаётся через values и не содержит реального registry по умолчанию:

| Параметр | По умолчанию | Назначение |
| --- | --- | --- |
| `image.repository` | `nomnom/payments` | Имя образа; registry задаётся окружением. |
| `image.registry` | `""` | Registry образа; `global.imageRegistry` имеет приоритет. |
| `image.tag` | `""` | Tag сборки. Пустое значение означает `latest` — задавайте явно. |
| `image.digest` | `""` | Digest образа; имеет приоритет над tag. |
| `image.pullPolicy` | `IfNotPresent` | Для kind позволяет использовать локально загруженный образ. |

Пример для kind с локально собранным образом (см. раздел «Контейнер» в [README](../README.md)):

```bash
kind load docker-image nomnom/payments:local
helm upgrade --install payments ops/helm --set image.tag=local
```

Если тестовое окружение не должно обращаться к registry вовсе, задайте
`--set image.pullPolicy=Never`. kind-specific значения в базовый chart не входят; при
необходимости они оформляются отдельным файлом вида `ops/helm/values-kind.yaml`.

## Конфигурация

Несекретная конфигурация процесса — версионированный файл ([ADR 17](../../../docs/adr/0017-cloud-native-application-runtime.md), раздел «Конфигурация» в [README](../README.md#конфигурация)). Chart рендерит его в `ConfigMap` ключом `config.yaml`, монтирует в `/etc/nomnom/config.yaml` и задаёт `CONFIG_FILE`; аннотация `checksum/config` перезапускает pod при изменении файла. Env `POD_NAME` (downward API) — `service.instance.id`.

```yaml
role: payments          # metadata.name файла; обязателен
config:
  spec:                 # попадает в файл как есть (toYaml)
    provider:
      kind: fake
    eventStore:
      snapshotEvery: 5
```

Файл получается таким:

```yaml
apiVersion: config.nomnom.dev/v1alpha1
kind: Payments
metadata:
  name: payments
spec:
  provider:
    kind: fake
  eventStore:
    snapshotEvery: 5
  identity:
    jwksFile: /etc/nomnom/identity/jwks.json             # задаёт chart
    userJwksFile: /etc/nomnom/user-identity/jwks.json    # задаёт chart
  telemetry:
    resourceAttributes:
      service.version: <image.tag>                       # задаёт chart
  # … остальные ключи config.spec из values
```

- Ключи — camelCase, как в [`config.example.yaml`](../config.example.yaml); неизвестный ключ процесс отвергает при старте с путём ключа. Значения по умолчанию values (порты, shutdown, пул, identity, SpiceDB) — `config.spec` в [`values.yaml`](helm/values.yaml).
- Chart владеет `spec.identity.jwksFile`, `spec.identity.userJwksFile` и `spec.telemetry.resourceAttributes["service.version"]`: такой ключ в values — ошибка `helm template`.
- Порты контейнера и `Service` chart берёт из `config.spec.server.port`, `config.spec.management.port` и `config.spec.provider.webhookPort`.
- `eventStore.snapshotEvery` обязателен: снапшот `Payment` пишется, когда commit проходит кратное ему значение ([локальный ADR 2](../docs/adr/0002-event-sourcing-for-payment.md)).
- Блока `env:` нет: переменные `SECTION__KEY` для несекретных настроек процесс не читает.

OpenTelemetry ([ADR 9](../../../docs/adr/0009-использовать-opentelemetry-и-grafana-observability-stack.md)) — секции `config.spec.telemetry`, `tracing`, `metrics` и `logging`, одинаковые во всех chart:

```yaml
config:
  spec:
    telemetry:
      endpoint: http://alloy.nomnom-observability.svc.cluster.local:4317
      protocol: grpc                 # или http/protobuf
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

Экспорт включают `tracing.enabled` и `metrics.enabled`; при включённом экспорте `endpoint` обязателен, без флагов ничего не экспортируется. `OTEL_*` chart не задаёт, процесс их не читает. JSON-логи в stdout пишутся всегда. Alloy sidecar не добавляется — collector разворачивается отдельно.

Секреты (пароли, API tokens, credentials БД) не помещаются в `values.yaml` и `ConfigMap` и полей в `spec` не имеют. Chart подключает их из внешних Kubernetes `Secret` (`database.uriSecret`, `spicedb.presharedKeySecret`, `eventStore.subjectKeysSecret`), остальные — `envFrom` и `extraEnv`:

```yaml
extraEnv:
  - name: PROVIDER__WEBHOOK_SECRET
    valueFrom:
      secretKeyRef:
        name: payments-provider
        key: webhook-secret
```

Secret-ресурсы chart не создаёт.

## Пересборка проекций

`payments` и `payment_operations` — проекции потоков `payment_events`, `payment_snapshots` — снапшоты ([локальный ADR 2](../docs/adr/0002-event-sourcing-for-payment.md#пересборка-проекций)). Команда запускается в поде процесса с его файлом конфигурации и секретами (сверяются только `apiVersion` и `kind` файла):

```bash
kubectl -n nomnom-payments exec deploy/payments -- python -m entrypoints.payments_admin.main projections check
```

```bash
kubectl -n nomnom-payments exec deploy/payments -- python -m entrypoints.payments_admin.main projections rebuild --snapshots
```

- `check` ничего не меняет и завершается с кодом 1, если нашёл расхождения. В выводе только ключи и вид расхождения: `missing`, `extra`, `differs`, `unreadable`.
- Сначала запускайте `check`: `rebuild` удаляет строки проекций без потока (`extra`), и после него расхождение уже не увидеть. Такие строки появляются, например, если образ без Event Sourcing выкачен поверх мигрированной схемы: откат образа после миграции не поддерживается.
- `rebuild` заменяет проекции одной транзакцией, а с `--snapshots` ещё и снапшоты. Запросы продолжают читать прежние строки, записи ждут, поэтому часть операций на это время может завершиться ошибкой и должна быть повторена. Повторной публикации событий нет: Outbox не затрагивается.
- Если таблицы заняты дольше 5 секунд или поток не читается, `rebuild` завершается с кодом 1 и откатывает всё.

## Забвение пользователя

Идентификатор пользователя в `requested_by` хранится зашифрованным ключом этого пользователя ([локальный ADR 2](../docs/adr/0002-event-sourcing-for-payment.md#crypto-shredding)). Удаление ключа делает записи нечитаемыми, события при этом не меняются:

```bash
kubectl -n nomnom-payments exec deploy/payments -- python -m entrypoints.payments_admin.main subjects forget <user-id>
```

```bash
kubectl -n nomnom-payments exec deploy/payments -- python -m entrypoints.payments_admin.main payments history <payment-id>
```

- `forget` необратим и идемпотентен. Идентификатор передаётся аргументом, поэтому остаётся в истории shell и может попасть в audit log Kubernetes.
- `history` показывает автора операции со статусом `known`, `forgotten` или `unknown` (операция старше поля).
- Команда затрагивает только журнал `payments`. Срок хранения бэкапов базы определяет реальное время забвения: в них остаётся ключ, защищённый KEK.
- `eventStore.subjectKeysSecret.name` обязателен. Ключ `keks` — JSON `{"<версия>": "<KEK>"}`, ключ `ref-key` — секрет поиска пользователя; значения случайные, не короче 32 символов (`openssl rand -base64 32`).

### Ротация KEK

1. Добавить новую версию в `keks`, не удаляя старую: `{"1": "<старый>", "2": "<новый>"}`, и перезапустить `payments`. Новые ключи оборачивает старшая версия.
2. Дождаться завершения rollout и запустить `subjects rewrap`. Команда идёт пачками, её можно повторять; код выхода 1 означает, что `remaining` ещё больше нуля.
3. Удалить старую версию из `keks` и перезапустить `payments`.

```bash
kubectl -n nomnom-payments exec deploy/payments -- python -m entrypoints.payments_admin.main subjects rewrap
```

Шифротекст в журнале не меняется: перешифровываются только ключи субъектов. `ref-key` при ротации не трогают: без него пользователя не найти для `forget`. Пока идёт шаг 1, под без новой версии не откроет ключи версии 2, поэтому `rewrap` запускается после rollout. KEK лучше менять после компрометации или по расписанию, а бэкапы со старой версией остаются читаемыми только этой версией.

## Security

По умолчанию контейнер запускается с `runAsNonRoot: true`, `allowPrivilegeEscalation: false`,
сбросом всех capabilities и seccomp profile `RuntimeDefault`. Dockerfile создаёт пользователя `app` с UID/GID `10001`, поэтому `podSecurityContext.runAsUser`/`runAsGroup` установлены в `10001`: без числового UID kubelet не может подтвердить `runAsNonRoot` для пользователя, заданного именем.

`readOnlyRootFilesystem` выключен и доступен через values: Python runtime и будущие зависимости могут использовать временные файлы; `PYTHONDONTWRITEBYTECODE=1` уже задан в образе.

## Runtime contract

Chart реализует [runtime contract](../../../docs/cloud-native-runtime.md) ([ADR 17](../../../docs/adr/0017-cloud-native-application-runtime.md)): контейнер объявляет технический порт `metrics` (`config.spec.management.port`, по умолчанию `9084`), а probes обращаются к health endpoints на нём. Порт `metrics` не входит в `Service`: kubelet probes и Alloy scrape идут напрямую в pod, имя порта задано моделью NetworkPolicy [ADR 16](../../../docs/adr/0016-запрещать-обход-internal-gateway-через-cilium-и-networkpolicy.md).

| Параметр | По умолчанию | Назначение |
| --- | --- | --- |
| `config.spec.management.port` | `9084` | Номер технического порта. |
| `config.spec.shutdown.readinessDelay` | `5s` | Сколько pod после `SIGTERM` продолжает обслуживать запросы с readiness `503`. |
| `config.spec.shutdown.drainTimeout` | `20s` | Deadline на завершение начатых запросов. |
| `config.spec.shutdown.telemetryFlushTimeout` | `5s` | Deadline на flush telemetry после drain. |
| `terminationGracePeriodSeconds` | `35` | Должен быть больше суммы delay, drain и flush; иначе `helm template` завершается ошибкой. |
| `startupProbe` | `/health/startup`, каждые 2s, до 30 попыток | Пока не пройдена, liveness и readiness не проверяются. |
| `livenessProbe` | `/health/live`, каждые 10s, 3 провала | Остаётся успешной во время shutdown. |
| `readinessProbe` | `/health/ready`, каждые 2s, 1 провал | Исключает pod из endpoints сразу после `SIGTERM`, в пределах readiness delay. |

Поля probes, кроме `enabled`, передаются в Kubernetes как есть. Приложение без HTTP-стека использует `grpc: { port: <номер технического порта>, service: <startup|liveness|readiness> }` вместо `httpGet`; `grpc` probe Kubernetes принимает только числовой порт.

## Service identity

Проверка projected ServiceAccount token ([ADR 27](../../../docs/adr/0027-service-identity-via-projected-serviceaccount-tokens.md), см. раздел «Service identity» в [README](../README.md#service-identity)) и user assertions BFF ([ADR 32](../../../docs/adr/0032-user-identity-via-bff-signed-assertions.md)) обязательны. ConfigMap `identity.jwksConfigMap` монтируется read-only каталогом `/etc/nomnom/identity`, `identity.userJwksConfigMap` — каталогом `/etc/nomnom/user-identity` (только ключ `jwks.json`); пути chart пишет в `spec.identity.jwksFile` и `spec.identity.userJwksFile` файла конфигурации.

| Параметр | По умолчанию | Назначение |
| --- | --- | --- |
| `config.spec.identity.issuer` | `""` | Issuer токенов кластера; обязателен. |
| `config.spec.identity.audience` | `nomnom.payments` | Audience контекста. |
| `config.spec.identity.serviceAccounts` | `""` | Allowlist `namespace/serviceaccount=service-name` через запятую; обязателен. |
| `config.spec.identity.userIssuer` | `""` | Ожидаемый `iss` user assertions BFF (`nomnom-bff`); обязателен. |
| `config.spec.identity.userDelegates` | `""` | Services через запятую, которым разрешено передавать user (`bff`); обязателен. |
| `identity.jwksConfigMap` | `""` | ConfigMap с ключом `jwks.json` (JWKS кластера); создаётся развёртыванием, не chart; обязателен. |
| `identity.userJwksConfigMap` | `""` | ConfigMap с ключом `jwks.json` (JWKS BFF); создаётся развёртыванием, не chart; обязателен. |

Если любой из этих параметров пуст, `helm template` завершается ошибкой `<параметр> is required`.

```bash
helm template payments ops/helm \
  --set identity.jwksConfigMap=cluster-jwks --set identity.userJwksConfigMap=bff-jwks \
  --set config.spec.identity.issuer=https://kubernetes.default.svc.cluster.local \
  --set config.spec.identity.serviceAccounts=nomnom/orders-worker=orders-worker \
  --set config.spec.identity.userIssuer=nomnom-bff --set config.spec.identity.userDelegates=bff
```

## Authorization (SpiceDB)

Проверка permissions в SpiceDB ([ADR 18](../../../docs/adr/0018-spicedb-for-authorization.md), см. раздел «Authorization» в [README](../README.md#authorization)) обязательна: без `config.spec.spicedb.endpoint` или `spicedb.presharedKeySecret` `helm template` завершается ошибкой. Контейнер получает `SPICEDB__PRESHARED_KEY` из `secretKeyRef` на `spicedb.presharedKeySecret`.

| Параметр | По умолчанию | Назначение |
| --- | --- | --- |
| `config.spec.spicedb.endpoint` | `""` | gRPC endpoint SpiceDB `host:port`; обязателен. |
| `config.spec.spicedb.insecure` | `false` | Plaintext gRPC без TLS; только kind и локальные окружения. |
| `config.spec.spicedb.timeout` | `2s` | Deadline одного `CheckPermission`, `<int>(ms|s|m)`. |
| `spicedb.presharedKeySecret.name` | `""` | Secret с preshared key; обязателен, создаётся развёртыванием, не chart. |
| `spicedb.presharedKeySecret.key` | `preshared-key` | Ключ в Secret. |

```bash
helm template payments ops/helm \
  --set config.spec.spicedb.endpoint=spicedb.nomnom-authz.svc.cluster.local:50051 \
  --set config.spec.spicedb.insecure=true \
  --set spicedb.presharedKeySecret.name=spicedb-preshared-key
```

## PostgreSQL

Хранилище payments ([ADR 8](../../../docs/adr/0008-storage-strategy-postgresql-and-mongodb.md), [локальный ADR 1](../docs/adr/0001-use-postgresql-for-payments-storage.md)). Connection URI PostgreSQL (`postgresql://` или `postgres://`; форма libpq `key=value` не принимается) передаётся из внешнего Secret `database.uriSecret.name`/`key` (env `DATABASE__URI`); без него переменная не задаётся и процесс не стартует. Процесс сам применяет недостающие forward-only SQL-миграции при старте, до открытия портов, библиотекой yoyo-migrations под одним session advisory lock PostgreSQL — сначала набор общих таблиц `messaging`, затем наборы slices ([README](../README.md#статус-реализации)): реплики не мешают друг другу, при ошибке миграции процесс завершается с кодом 1. Отдельного шага или init container нет.

Пулер перед базой — только session mode; replicas × maxConns должна помещаться в `pool_size` пулера ([README](../README.md#пулер-postgresql)). Лимиты пула — `config.spec.database`; длительности — `<int>(ms|s|m)`, chart отклоняет другой формат и `minConns` вне 0–1.

| Значение | По умолчанию | Назначение |
| --- | --- | --- |
| `database.uriSecret.name` | `""` | Secret с connection URI; создаётся развёртыванием, не chart. |
| `database.uriSecret.key` | `dsn` | Ключ в Secret. |
| `config.spec.database.maxConns` | `10` | Размер пула процесса. Миграции при старте идут на отдельном соединении до открытия пула. |
| `config.spec.database.minConns` | `1` | Минимум соединений пула, 0 или 1. |
| `config.spec.database.maxConnLifetime` | `30m` | Lifetime соединения, jitter 0–5% добавляет psycopg_pool. |
| `config.spec.database.maxConnIdleTime` | `5m` | Закрытие простаивающего соединения. |
| `config.spec.database.connectTimeout` | `5s` | `connect_timeout` libpq. |
| `config.spec.database.acquireTimeout` | `5s` | Ожидание соединения пула. |

Роль базы должна владеть схемой: создаёт таблицы `payments_outbox` (и publication `payments_outbox`) и `inbox` (набор `messaging`), `payment_events`, `payment_snapshots`, `payments` и `payment_operations` (набор `payments`), служебные таблицы yoyo-migrations каждого набора, названные по его storage id (контракт схемы, не зависит от раскладки кода) — `_yoyo_migration_messaging`, `_yoyo_log_messaging`, `_yoyo_version_messaging`, `_yoyo_migration_payments`, `_yoyo_log_payments`, `_yoyo_version_payments` — и общую `yoyo_lock` (yoyo держит в ней строку только на время обновления своей внутренней схемы). Пример для kind:

```bash
helm upgrade --install payments ops/helm \
  --set database.uriSecret.name=payments-postgres
```

## CDC Outbox

Outbox `payments_outbox` публикует коннектор Debezium `payments-outbox` в Kafka Connect
([ADR 37](../../../docs/adr/0037-outbox-delivery-via-debezium-cdc.md)); процесса relay нет.
При `cdc.enabled` chart создаёт ресурс Strimzi `KafkaConnector` `payments-outbox` в namespace
KafkaConnect `cdc.connect.namespace` с label `strimzi.io/cluster: <cdc.connect.cluster>`:
Cluster Operator регистрирует коннектор и сам перезапускает упавшие задачи (`autoRestart`). Конфигурация — `cdc.connector.config` (контракт Outbox:
publication и слот `payments_outbox`, топик из поля `topic`, ключ `aggregate_id`, заголовки
Event Envelope) поверх `cdc.database`. `cdc.database.password` — ссылка config provider
Connect (`${dir:/mnt/cdc:payments-password}`), а не пароль: Secret `cdc-credentials`
монтирует сам Connect. Publication создаёт миграция `infrastructure/repository/migrations/00001_outbox.sql`, слот — коннектор;
PostgreSQL нужен `wal_level = logical`.

```bash
helm template payments ops/helm \
  --set cdc.enabled=true \
  --set cdc.connect.cluster=kafka-connect --set cdc.connect.namespace=nomnom-infra \
  --set cdc.schemaRegistryUrl=http://schema-registry.nomnom-infra.svc.cluster.local:8081 \
  --set cdc.database.hostname=postgres.nomnom-data.svc.cluster.local \
  --set cdc.database.name=payments --set cdc.database.user=payments \
  --set 'cdc.database.password=${dir:/mnt/cdc:payments-password}'
```

Состояние коннектора — `GET /connectors/payments-outbox/status`; запись, нарушающая контракт,
останавливает его до `POST /connectors/payments-outbox/restart?includeTasks=true`.

## Ресурсы

`resources.requests` и `resources.limits` не заданы: профиль потребления ещё не измерен.
Значения задаются environment values после нагрузочной проверки.

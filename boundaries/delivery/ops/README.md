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
| `Deployment` | Контейнер приложения и init container миграций `delivery-migrate` того же образа; все изменяемые параметры в `values.yaml`. |
| `Service` (`ClusterIP`, порт `8083`) | Backend target для internal gateway и технических механизмов Kubernetes. |
| `ServiceAccount` | Отдельный аккаунт с `automountServiceAccountToken: false`. |
| `ConfigMap` | Несекретная конфигурация из `env`; создаётся только если `env` не пуст. |

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

Приложение слушает порт `8083` (переменная `SERVER__PORT`); `service.targetPort` должен
совпадать с портом процесса. Несекретная конфигурация передаётся через `env` и попадает в
`ConfigMap`:

```yaml
env:
  OTEL_SERVICE_NAME: delivery
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
[ADR 0001 контекста](../docs/adr/0001-postgresql-хранилище-delivery.md)). Контейнер и init
container `migrate` (`/usr/local/bin/delivery-migrate`, `database.migrate: true`) получают
`DATABASE__URL` из Secret `database.urlSecret` (по умолчанию `delivery-postgres`, ключ `dsn`)
и `DATABASE__MAX_CONNS` (`database.maxConns`). Secret создаёт развёртывание: в kind —
`ops/kind/scripts/postgres.sh` вместе с базой `postgres-delivery`. Без Secret init container не
рендерится, а процесс завершается ошибкой startup (`DATABASE__URL is required`).

## Service identity

Проверку projected ServiceAccount token ([ADR 27](../../../docs/adr/0027-service-identity-via-projected-serviceaccount-tokens.md))
включает `identity.jwksConfigMap`: ConfigMap с ключом `jwks.json` (создаётся развёртыванием из
`kubectl get --raw /openid/v1/jwks`) монтируется read-only в `/etc/nomnom/identity/jwks.json`, а
контейнер получает `IDENTITY__ISSUER`, `IDENTITY__AUDIENCE`, `IDENTITY__JWKS_FILE` и
`IDENTITY__SERVICE_ACCOUNTS`. Без `jwksConfigMap` переменные не задаются и приложение отклоняет все
gRPC-вызовы `UNAUTHENTICATED`.

```bash
helm template delivery boundaries/delivery/ops/helm \
  --set identity.issuer=https://kubernetes.default.svc.cluster.local \
  --set identity.serviceAccounts=nomnom/orders-worker=orders-worker \
  --set identity.jwksConfigMap=cluster-jwks
```

JWKS читается при старте: после обновления ConfigMap нужен rollout restart.

User assertions BFF ([ADR 32](../../../docs/adr/0032-user-identity-via-bff-signed-assertions.md))
включает `identity.userJwksConfigMap` вместе с `identity.jwksConfigMap`: ключ `jwks.json` (публичные
ключи BFF, в kind — ConfigMap `bff-jwks`) монтируется read-only в
`/etc/nomnom/user-identity/jwks.json`, а контейнер получает `IDENTITY__USER_ISSUER`
(`identity.userIssuer`), `IDENTITY__USER_JWKS_FILE` и `IDENTITY__USER_DELEGATES`
(`identity.userDelegates`). По умолчанию значения пусты, и вызовы с `x-nomnom-user-assertion`
отклоняются.

## SpiceDB

Авторизацию в SpiceDB ([ADR 18](../../../docs/adr/0018-spicedb-for-authorization.md)) включает
`spicedb.endpoint`: контейнер получает `SPICEDB__ENDPOINT`, `SPICEDB__INSECURE`, `SPICEDB__TIMEOUT`
и `SPICEDB__PRESHARED_KEY` из Secret `spicedb.presharedKeySecret` (`secretKeyRef`, по умолчанию
ключ `preshared-key`). Secret создаётся развёртыванием; без его имени при заданном endpoint chart
не рендерится. Без endpoint переменные не задаются, и приложение не стартует:
`SPICEDB__ENDPOINT` обязателен. `spicedb.insecure: true` (plaintext) — только для kind.

```bash
helm template delivery boundaries/delivery/ops/helm \
  --set spicedb.endpoint=spicedb.nomnom-authz.svc:50051 \
  --set spicedb.insecure=true \
  --set spicedb.presharedKeySecret.name=spicedb-preshared-key
```

## Security

По умолчанию контейнер запускается с `runAsNonRoot: true`, `allowPrivilegeEscalation: false`,
сбросом всех capabilities и seccomp profile `RuntimeDefault`. Dockerfile создаёт пользователя `app` с UID/GID `10001`, поэтому `podSecurityContext.runAsUser`/`runAsGroup` установлены в `10001`: без числового UID kubelet не может подтвердить `runAsNonRoot` для пользователя, заданного именем.

`readOnlyRootFilesystem` выключен и доступен через values: пока не подтверждено, что приложение и будущий profiler не пишут во временную файловую систему.

## Runtime contract

Chart реализует [runtime contract](../../../docs/cloud-native-runtime.md) ([ADR 17](../../../docs/adr/0017-cloud-native-application-runtime.md)): контейнер объявляет технический порт `metrics` (`management.port`, по умолчанию `9083`), а probes обращаются к health endpoints на нём. Порт `metrics` не входит в `Service`: kubelet probes и Alloy scrape идут напрямую в pod, имя порта задано моделью NetworkPolicy [ADR 16](../../../docs/adr/0016-запрещать-обход-internal-gateway-через-cilium-и-networkpolicy.md).

| Параметр | По умолчанию | Назначение |
| --- | --- | --- |
| `management.port` | `9083` | Номер технического порта; передаётся приложению как `MANAGEMENT__PORT`. |
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

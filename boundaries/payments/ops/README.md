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
| `ConfigMap` | Несекретная конфигурация из `env`; создаётся только если `env` не пуст. |

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

Приложение слушает порт `8084` (переменная `SERVER__PORT`); `service.targetPort` должен
совпадать с портом процесса. Несекретная конфигурация передаётся через `env` и попадает в
`ConfigMap`:

```yaml
env:
  OTEL_SERVICE_NAME: payments
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
      name: payments-secrets

extraEnv:
  - name: DATABASE_PASSWORD
    valueFrom:
      secretKeyRef:
        name: payments-database
        key: password
```

Secret-ресурсы chart не создаёт.

## Security

По умолчанию контейнер запускается с `runAsNonRoot: true`, `allowPrivilegeEscalation: false`,
сбросом всех capabilities и seccomp profile `RuntimeDefault`. Dockerfile создаёт пользователя `app` с UID/GID `10001`, поэтому `podSecurityContext.runAsUser`/`runAsGroup` установлены в `10001`: без числового UID kubelet не может подтвердить `runAsNonRoot` для пользователя, заданного именем.

`readOnlyRootFilesystem` выключен и доступен через values: Python runtime и будущие зависимости могут использовать временные файлы; `PYTHONDONTWRITEBYTECODE=1` уже задан в образе.

## Runtime contract

Chart реализует [runtime contract](../../../docs/cloud-native-runtime.md) ([ADR 17](../../../docs/adr/0017-cloud-native-application-runtime.md)): контейнер объявляет технический порт `metrics` (`management.port`, по умолчанию `9084`), а probes обращаются к health endpoints на нём. Порт `metrics` не входит в `Service`: kubelet probes и Alloy scrape идут напрямую в pod, имя порта задано моделью NetworkPolicy [ADR 16](../../../docs/adr/0016-запрещать-обход-internal-gateway-через-cilium-и-networkpolicy.md).

| Параметр | По умолчанию | Назначение |
| --- | --- | --- |
| `management.port` | `9084` | Номер технического порта; передаётся приложению как `MANAGEMENT__PORT`. |
| `shutdown.readinessDelaySeconds` | `5` | `SHUTDOWN__READINESS_DELAY`: сколько pod после `SIGTERM` продолжает обслуживать запросы с readiness `503`. |
| `shutdown.drainTimeoutSeconds` | `20` | `SHUTDOWN__DRAIN_TIMEOUT`: deadline на завершение начатых запросов. |
| `terminationGracePeriodSeconds` | `30` | Должен быть больше суммы delay и drain; иначе `helm template` завершается ошибкой. |
| `startupProbe` | `/health/startup`, каждые 2s, до 30 попыток | Пока не пройдена, liveness и readiness не проверяются. |
| `livenessProbe` | `/health/live`, каждые 10s, 3 провала | Остаётся успешной во время shutdown. |
| `readinessProbe` | `/health/ready`, каждые 2s, 1 провал | Исключает pod из endpoints сразу после `SIGTERM`, в пределах readiness delay. |

Поля probes, кроме `enabled`, передаются в Kubernetes как есть. Приложение без HTTP-стека использует `grpc: { port: <номер технического порта>, service: <startup|liveness|readiness> }` вместо `httpGet`; `grpc` probe Kubernetes принимает только числовой порт.

## Service identity

Проверка projected ServiceAccount token ([ADR 27](../../../docs/adr/0027-service-identity-via-projected-serviceaccount-tokens.md), см. раздел «Service identity» в [README](../README.md#service-identity)) включается заданием `identity.jwksConfigMap`. Тогда ConfigMap монтируется read-only каталогом `/etc/nomnom/identity` (только ключ `jwks.json`), а контейнер получает `IDENTITY__ISSUER`, `IDENTITY__AUDIENCE`, `IDENTITY__JWKS_FILE=/etc/nomnom/identity/jwks.json` и `IDENTITY__SERVICE_ACCOUNTS`. Без `jwksConfigMap` переменные не задаются и приложение отклоняет все вызовы (fail closed).

| Параметр | По умолчанию | Назначение |
| --- | --- | --- |
| `identity.issuer` | `""` | Issuer токенов кластера; обязателен при `jwksConfigMap`. |
| `identity.audience` | `nomnom.payments` | Audience контекста. |
| `identity.serviceAccounts` | `""` | Allowlist `namespace/serviceaccount=service-name` через запятую; обязателен при `jwksConfigMap`. |
| `identity.jwksConfigMap` | `""` | ConfigMap с ключом `jwks.json` (JWKS кластера); создаётся развёртыванием, не chart. |

При `jwksConfigMap` без `issuer`, `audience` или `serviceAccounts` `helm template` завершается ошибкой.

```bash
helm template payments ops/helm \
  --set identity.jwksConfigMap=cluster-jwks \
  --set identity.issuer=https://kubernetes.default.svc.cluster.local \
  --set identity.serviceAccounts=nomnom/orders-worker=orders-worker
```

## Authorization (SpiceDB)

Проверка permissions в SpiceDB ([ADR 18](../../../docs/adr/0018-spicedb-for-authorization.md), см. раздел «Authorization» в [README](../README.md#authorization)) включается заданием `spicedb.endpoint`. Тогда контейнер получает `SPICEDB__ENDPOINT`, `SPICEDB__INSECURE`, `SPICEDB__TIMEOUT` и `SPICEDB__PRESHARED_KEY` из `secretKeyRef` на `spicedb.presharedKeySecret`. Без `endpoint` переменные не задаются, и приложение не стартует: `SPICEDB__ENDPOINT` обязателен.

| Параметр | По умолчанию | Назначение |
| --- | --- | --- |
| `spicedb.endpoint` | `""` | gRPC endpoint SpiceDB `host:port`. |
| `spicedb.insecure` | `false` | Plaintext gRPC без TLS; только kind и локальные окружения. |
| `spicedb.timeout` | `2s` | Deadline одного `CheckPermission`, `<int>(ms|s|m)`. |
| `spicedb.presharedKeySecret.name` | `""` | Secret с preshared key; обязателен при `endpoint`, создаётся развёртыванием, не chart. |
| `spicedb.presharedKeySecret.key` | `preshared-key` | Ключ в Secret. |

При `endpoint` без `presharedKeySecret.name`/`key` или с невалидным `timeout` `helm template` завершается ошибкой.

```bash
helm template payments ops/helm \
  --set spicedb.endpoint=spicedb.nomnom-authz.svc.cluster.local:50051 \
  --set spicedb.insecure=true \
  --set spicedb.presharedKeySecret.name=spicedb-preshared-key
```

## Ресурсы

`resources.requests` и `resources.limits` не заданы: профиль потребления ещё не измерен.
Значения задаются environment values после нагрузочной проверки.

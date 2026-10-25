# Ops: bff

Каталог `ops/` содержит операционные файлы приложения `bff` — всё, что нужно для его
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
| `Service` (`ClusterIP`, порт `3010`) | Backend target для internal gateway и технических механизмов Kubernetes. |
| `ServiceAccount` | Отдельный аккаунт с `automountServiceAccountToken: false`. |
| `ConfigMap` | Несекретная конфигурация из `env`; создаётся только если `env` не пуст. |

Существующее архитектурное правило сохраняется: бизнес-вызовы между сервисами не обращаются
к `Service` напрямую и проходят через internal gateway. Ingress, `HTTPRoute`/`GRPCRoute`,
базы данных, Kafka, Temporal, service mesh и observability collector в chart не входят.

## Команды

Команды выполняются из каталога приложения (`boundaries/bff`):

```bash
helm lint ops/helm
```

```bash
helm template bff ops/helm
```

```bash
helm upgrade --install bff ops/helm
```

```bash
helm uninstall bff
```

Из корня репозитория путь к chart — `boundaries/bff/ops/helm`.

## Образ

Runtime-образ полностью задаётся через values и не содержит реального registry по умолчанию:

| Параметр | По умолчанию | Назначение |
| --- | --- | --- |
| `image.repository` | `""` (не определён) | Имя образа; registry задаётся окружением. |
| `image.tag` | `""` | Tag сборки. Пустое значение означает `latest` — задавайте явно. |
| `image.pullPolicy` | `IfNotPresent` | Для kind позволяет использовать локально загруженный образ. |

Образ собирает [`Dockerfile`](../Dockerfile) (см. README BFF, «Статус контейнеризации»). В kind
его собирает и загружает Skaffold, а values окружения лежат в
[`ops/kind/values/bff.yaml`](../../../ops/kind/values/bff.yaml) (`pullPolicy: Never`); в базовый chart
kind-specific значения не входят.

## Конфигурация

Router в образе слушает `0.0.0.0:3010` ([`config.production.yaml`](../config.production.yaml));
`service.targetPort` должен совпадать с портом процесса. Несекретная конфигурация передаётся через `env` и попадает в
`ConfigMap`:

```yaml
env:
  BFF__METRICS_OTLP_ENABLED: "true"
  BFF__OTLP_METRICS_ENDPOINT: http://alloy.nomnom-observability.svc.cluster.local:4318
  BFF__OTLP_TRACES_ENDPOINT: http://alloy.nomnom-observability.svc.cluster.local:4318
  BFF__DEPLOYMENT_ENVIRONMENT: kind
```

`env.BFF__METRICS_OTLP_ENABLED` обязателен: chart падает без него, а с `"true"` требует
`env.BFF__OTLP_METRICS_ENDPOINT`. Router metrics уходят по OTLP/HTTP; какие из них остаются, решает Alloy
([ops/grafana-cloud](../../../ops/grafana-cloud/README.md)).

Адрес Alloy и другие значения OpenTelemetry задаются окружением и не хардкодятся в chart;
Alloy sidecar не добавляется — collector разворачивается отдельно.

Секреты (пароли, API tokens, credentials БД и Kafka, BSR tokens) не помещаются в
`values.yaml` и `ConfigMap`. Они подключаются из внешних Kubernetes `Secret`:

```yaml
envFrom:
  - secretRef:
      name: bff-secrets

extraEnv:
  - name: DATABASE_PASSWORD
    valueFrom:
      secretKeyRef:
        name: bff-database
        key: password
```

Secret-ресурсы chart не создаёт.

## Security

По умолчанию контейнер запускается с `runAsNonRoot: true`, `allowPrivilegeEscalation: false`,
сбросом всех capabilities и seccomp profile `RuntimeDefault`; `podSecurityContext.runAsUser` и
`runAsGroup` — 65532 (`nonroot` образа distroless).

`readOnlyRootFilesystem` выключен и доступен через values до проверки поведения Cosmo Router и plugin.

## Probes

Liveness и readiness включены и используют штатные endpoints Cosmo Router: `/health/live` и
`/health/ready` (execution config и plugin загружены). Router обслуживает их только на порту
GraphQL, поэтому probes идут на порт `http`, а не на технический порт `metrics` из
[ADR 17](../../../docs/adr/0017-cloud-native-application-runtime.md) — это явное исключение для BFF.
`/health/startup` у Router нет; старт Router занимает доли секунды, поэтому `startupProbe` не задан.

## Ресурсы

`resources.requests` и `resources.limits` не заданы: профиль потребления ещё не измерен.
Значения задаются environment values после нагрузочной проверки.

## Образ BFF

Образ всего BFF (Cosmo Router с закреплёнными версией и SHA-256, plugin, execution config)
собирает [`Dockerfile`](../Dockerfile) по
[ADR 10](../../../docs/adr/0010-использовать-multi-stage-container-builds.md). Токены control plane и
composition в образ не попадают: композиция локальная; если они появятся, они передаются внешним
`Secret`, а не values.

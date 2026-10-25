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

Пока образ BFF не определён, локальной сборки для kind нет. После появления образа он
загружается в kind командой `kind load docker-image <image>:<tag>` и задаётся через
`--set image.repository=<image> --set image.tag=<tag>`. Если тестовое окружение не должно
обращаться к registry вовсе, задайте `--set image.pullPolicy=Never`. kind-specific значения в
базовый chart не входят; при необходимости они оформляются отдельным файлом вида
`ops/helm/values-kind.yaml`.

## Конфигурация

Приложение слушает порт `3010` (переменная `LISTEN_ADDR`); `service.targetPort` должен
совпадать с портом процесса. Несекретная конфигурация передаётся через `env` и попадает в
`ConfigMap`:

```yaml
env:
  OTEL_SERVICE_NAME: bff
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
сбросом всех capabilities и seccomp profile `RuntimeDefault`. `runAsUser` не задан: UID runtime-образа BFF неизвестен, пока образ не определён. После его появления UID переносится в `podSecurityContext`.

`readOnlyRootFilesystem` выключен и доступен через values до проверки поведения Cosmo Router и plugin.

## Probes

Liveness и readiness probes поддерживаются chart, но отключены: приложение пока не реализует
health contract, а `/hello` таким контрактом не является. Contract определён в
[документации cloud-native runtime](../../../docs/cloud-native-runtime.md) и
[ADR 17](../../../docs/adr/0017-cloud-native-application-runtime.md): health обслуживается на
отдельном техническом порту `metrics`, а не на бизнес-порту. Probes включаются сразу после
реализации contract в приложении; объявление технического порта, `startupProbe` и
`terminationGracePeriodSeconds` добавляются в chart тем же изменением:

```yaml
startupProbe:
  enabled: true
  httpGet:
    path: /health/startup
    port: metrics
livenessProbe:
  enabled: true
  httpGet:
    path: /health/live
    port: metrics
readinessProbe:
  enabled: true
  httpGet:
    path: /health/ready
    port: metrics
```

Приложение без HTTP-стека использует `grpc: { port: <номер технического порта>, service: <startup|liveness|readiness> }`
вместо `httpGet`; `grpc` probe Kubernetes принимает только числовой порт.

BFF использует Cosmo Router, который сам обслуживает health endpoints. Если вынести их на
технический порт средствами Cosmo Router нельзя, расхождение фиксируется как явное исключение
в документации BFF.

## Ресурсы

`resources.requests` и `resources.limits` не заданы: профиль потребления ещё не измерен.
Значения задаются environment values после нагрузочной проверки.

## Статус образа BFF

Образ всего BFF (Cosmo Router, plugin, execution config) ещё не определён: по
[ADR 10](../../../docs/adr/0010-использовать-multi-stage-container-builds.md) его Dockerfile отложен до
pinning версии и checksum Cosmo Router. Существующий `plugins/bff/Dockerfile` собирает только
plugin и не является образом BFF. Поэтому `image.repository` по умолчанию пуст, а
`helm upgrade --install` даст Deployment, который не запустится до задания образа.

При появлении образа нужно учесть:

- `config.yaml` задаёт `listen_addr: localhost:3010`; в контейнере router должен слушать
  `0.0.0.0:3010` (production `config.yaml` или переменная `LISTEN_ADDR`);
- `dev_mode` в production выключается;
- UID runtime-пользователя образа переносится в `podSecurityContext.runAsUser`, иначе проверка
  `runAsNonRoot` не сможет подтвердить непривилегированного пользователя по имени;
- токены control plane и composition передаются внешним `Secret`, а не values.

# Design

## Формат

Файл конфигурации процесса имеет вид ресурса Kubernetes, но в Kubernetes API не регистрируется: это формат файла, а не CRD.

```yaml
apiVersion: config.nomnom.dev/v1alpha1
kind: Orders
metadata:
  name: orders-worker
spec:
  management:
    port: 9081
  shutdown:
    readinessDelay: 5s
    drainTimeout: 20s
  telemetry:
    endpoint: http://alloy.observability.svc:4317
    protocol: grpc
    serviceName: orders-worker
    resourceAttributes:
      deployment.environment.name: kind
  tracing:
    enabled: true
  metrics:
    enabled: true
  logging:
    level: info
  temporal:
    hostPort: temporal.nomnom-infra.svc.cluster.local:7233
    namespace: default
  fulfillment:
    acceptanceTimeout: 10m
```

- `apiVersion` — `config.nomnom.dev/v1alpha1` для всех сервисов. Несовместимое изменение структуры `spec` объявляется новой версией; процесс принимает только версии, которые знает.
- `kind` — сервис: `Orders`, `Delivery`, `Payments`, `Restaurants`, `ControlPlane`. Все роли сервиса делят один kind, потому что делят один пакет `pkg/config`.
- `metadata.name` — роль процесса: `orders`, `orders-worker`, `orders-consumer`, `delivery`, `delivery-worker`, `payments`, `restaurants`, `restaurants-consumer`, `controlplane`. Процесс сверяет его со своей ролью, поэтому ConfigMap чужой роли не будет принят молча. Операторские команды (`orders-admin`, `delivery-admin`, `payments_admin`, `restaurants-admin`) запускаются через `kubectl exec` в pod другой роли и читают его файл, поэтому сверяют только `apiVersion` и `kind`.
- `spec` — секции процесса. Ключи — camelCase, длительности — строки Go-формата (`500ms`, `5s`, `2m`, `1h`), как сейчас.
- Неизвестный ключ на любом уровне, включая верхний (`status`, опечатка в секции), — ошибка старта с путём ключа. Процесс читает только нужные ему секции `spec`, но проверяет на неизвестные ключи весь `spec` своего kind: секции других ролей того же kind допустимы.

Формат файла — только YAML. Delivery переходит с TOML на YAML.

## Источники значений

| Что | Откуда | Если нет |
|---|---|---|
| Несекретные настройки | файл `CONFIG_FILE` | действует явный `DEFAULT_*`, если он есть; иначе ошибка старта |
| Секреты | env из Secret под текущими именами | ошибка старта, если секрет обязателен процессу |
| `service.instance.id` | env `POD_NAME` (Downward API) | ошибка старта |
| Путь к файлу | env `CONFIG_FILE` | ошибка старта |

Переменные `SECTION__KEY` для несекретных настроек больше не читаются: у настройки один источник. Секреты — по сервисам:

| Сервис | Переменные |
|---|---|
| orders | `POSTGRES__DSN`, `SPICEDB__PRESHARED_KEY` |
| delivery | `DATABASE__URI`, `VALKEY__URL`, `SPICEDB__PRESHARED_KEY` |
| payments | `DATABASE__URI`, `SPICEDB__PRESHARED_KEY`, `EVENT_STORE__SUBJECT_KEKS`, `EVENT_STORE__SUBJECT_REF_KEY`, `PROVIDER__WEBHOOK_SECRET`, `PROVIDER__STRIPE_API_KEY` |
| restaurants | `MONGODB__URI`, `VALKEY__URL`, `SPICEDB__PRESHARED_KEY` |
| controlplane | нет |

В `spec` полей для секретов нет: ключ `postgres.dsn` в файле — неизвестный ключ и ошибка старта, поэтому секрет нельзя по ошибке положить в ConfigMap.

Значения по умолчанию остаются там, где они есть сейчас (`DEFAULT_*` для портов, таймаутов, пулов), и не появляются там, где их нет: обязательные без значения по умолчанию (`cancellation.scheduledCutoff`, `scheduledOrder.*`, `simulator.*`, `identity.*` для процессов с gRPC API, `spicedb.endpoint`) остаются обязательными.

## Телеметрия

| Ключ | Значения | По умолчанию |
|---|---|---|
| `telemetry.endpoint` | URL OTLP коллектора | нет; обязателен, если включён хотя бы один из `tracing` и `metrics` |
| `telemetry.protocol` | `grpc`, `http/protobuf` | `grpc` |
| `telemetry.serviceName` | `service.name` | имя процесса по умолчанию, как сейчас (`AppServiceName` и аналоги) |
| `telemetry.resourceAttributes` | map атрибутов resource | пусто |
| `tracing.enabled` | `true`, `false` | `false` |
| `metrics.enabled` | `true`, `false` | `false` |
| `logging.level` | `debug`, `info`, `warn`, `error` | `info` |

- Процесс передаёт SDK endpoint, протокол и resource явно. `OTEL_*` и `LOG__LEVEL` процесс не читает, chart их не задаёт. Если SDK языка сам читает `OTEL_*`, явные параметры имеют приоритет.
- Значения по умолчанию для `endpoint` нет: `localhost:4317` из примера Keycloak — подстановка адреса, которого в pod нет.
- Сейчас трассы и метрики экспортируются, когда задан `OTEL_EXPORTER_OTLP_ENDPOINT`. Теперь экспорт включается явно флагами; endpoint без флагов не включает экспорт.
- `service.version` — revision сборки, как сейчас; chart добавляет `service.version` из `image.tag` в `resourceAttributes`. `service.instance.id` — env `POD_NAME`.
- `resourceAttributes` не может переопределить `service.name` и `service.instance.id`: такой ключ — ошибка старта.
- Логи остаются JSON в stdout, их собирает Alloy.

## Загрузка по языкам

Общий порядок: прочитать `CONFIG_FILE` → проверить `apiVersion`, `kind`, `metadata.name` → строго декодировать `spec` → подставить секреты из env → существующая проверка значений.

| Сервис | Как |
|---|---|
| orders, controlplane | `go.yaml.in/yaml/v3` с `KnownFields(true)` в структуры с тегами `yaml`; viper удаляется. `time.Duration` декодируется из строки. |
| delivery | crate `config` с feature `yaml` вместо `toml`, `#[serde(deny_unknown_fields, rename_all = "camelCase")]`; env-источники кроме секретов удаляются. |
| payments | `yaml.safe_load` и pydantic-модели с `extra="forbid"` и camelCase-алиасами; секреты — `BaseSettings` только из env. |
| restaurants | пакет `yaml` и `z.strictObject` на каждом уровне; секреты — из `process.env`. |

Ошибка содержит путь ключа (`spec.shutdown.drainTimout: unknown field`) и завершает процесс с non-zero exit до открытия портов.

## Helm

Chart рендерит файл в ConfigMap с ключом `config.yaml`, монтирует его в `/etc/nomnom/config.yaml` и задаёт `CONFIG_FILE`. Аннотация `checksum/config` перезапускает pod при изменении файла.

```yaml
# values.yaml
config:
  spec:
    shutdown:
      readinessDelay: 5s
    telemetry:
      endpoint: ""
    temporal:
      hostPort: ""
# values-worker.yaml переопределяет секции роли
```

- `apiVersion`, `kind` и `metadata.name` задаёт chart: kind — константа chart, имя — роль из `values-<role>.yaml`.
- `config.spec` попадает в файл как есть (`toYaml`). Chart сам дописывает только то, чем владеет: пути смонтированных JWKS и token dir в `spec.identity`, `service.version` в `spec.telemetry.resourceAttributes`. Если такой ключ задан в values, `helm template` падает.
- Структурные блоки values, которые сейчас превращаются в env (`spicedb.endpoint`, `spicedb.insecure`, `postgres.maxConns` и т. п., `telemetry.*`), переезжают в `config.spec`. На уровне chart остаются только ссылки на Secret (`spicedb.presharedKeySecret`, `postgres.dsnSecret`) и имена ConfigMap JWKS.
- Блок values `env:` для несекретных настроек удаляется. `envFrom` и `extraEnv` остаются для секретов окружения.
- `ops/kind/values/*.yaml` сервисов переносят `env:` в `config.spec`.

## Тесты

- Unit-тесты загрузчика в каждом сервисе: неверные `apiVersion`, `kind`, `metadata.name`; неизвестный ключ в `spec` и на верхнем уровне; секрет в файле; нет `CONFIG_FILE`; секрет из env; правила `telemetry` и неизвестный `protocol`.
- `ops/runtime-contract`: процессы стартуют с временным файлом конфигурации; сценарии fail fast — `shutdown.drainTimeout: 0ms` в файле, неизвестный ключ, нет `CONFIG_FILE`.
- `helm template` каждого chart: файл рендерится с ролью и путями JWKS, ключ, которым владеет chart, в values — ошибка.
- Kind e2e — один прогон в конце: сервисы стартуют, трассы и метрики доходят до Alloy.

## Документация

Новый ADR не создаётся:

- ADR 17 — абзац о конфигурации: версионированный файл процесса, секреты в env, отказ от env для несекретных настроек;
- ADR 9 — настройка телеметрии из файла вместо `OTEL_*`, явные флаги экспорта;
- `docs/cloud-native-runtime.md`, `docs/observability.md`, README и `ops/README.md` сервисов, `ops/kind/README.md`.

## Риски

- `toYaml` в Helm пишет большие целые как `1e+06`, потому что values читаются через JSON в `float64`. Загрузчик примет такое значение только для float-поля. Целые настройки в `spec` сейчас малы (порты, размеры пулов, страницы); `helm template`-тест проверяет файл каждого chart.
- После перехода процесс с переменной `SECTION__KEY` молча её игнорирует. Это закрывает проверка `helm template`: chart больше не рендерит такие env, а `env:` удалён из values.
- `ops/kind/scripts/e2e.sh` (CI) env сервисов не задаёт и не меняется.
- Локальный запуск (`make run`) ссылается на `config.example.yaml` через `CONFIG_FILE` и задаёт `POD_NAME`.

## Порядок

Одно изменение для всех пяти сервисов: формат и загрузчик в `pkg/config`, телеметрия, chart и kind values, runtime-contract, документация. Проверка — один раз в конце: `make check` сервисов, `helm template`, runtime-contract, kind e2e.

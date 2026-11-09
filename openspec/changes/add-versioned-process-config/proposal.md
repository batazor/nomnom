# Proposal

## Why

Сейчас каждый процесс собирает конфигурацию из десятков переменных `SECTION__KEY`, а chart раскладывает values по env через ConfigMap и явные `env:`. У такой схемы несколько проблем:

- опечатка в имени переменной не видна: процесс её не читает и стартует со значением по умолчанию или падает с непонятной ошибкой про другой ключ;
- у конфигурации нет версии, поэтому несовместимое изменение структуры нельзя ни обнаружить, ни объявить;
- одна и та же настройка задаётся в разных местах: в `env:` values, в структурных блоках chart (`identity`, `spicedb`, `postgres`, `telemetry`) и в `OTEL_*`, а ключи `telemetry` различаются между chart (`otlpEndpoint` и `endpoint`, `logLevel` только у части);
- телеметрию настраивают стандартные `OTEL_*`, которые SDK читают неявно, а включить или выключить трассы отдельно от endpoint нельзя.

Нужен один версионированный файл конфигурации процесса в формате ресурса Kubernetes — по образцу CR Keycloak: `apiVersion`, `kind`, `metadata`, `spec`.

## What Changes

- **BREAKING** Несекретная конфигурация процесса задаётся только файлом `CONFIG_FILE` вида `apiVersion: config.nomnom.dev/v1alpha1`, `kind: <Service>`, `metadata.name: <роль>`, `spec: {…}`. Переменные `SECTION__KEY` для несекретных настроек больше не читаются.
- Процесс падает на старте, если файла нет, `apiVersion` или `kind` не совпадают, `metadata.name` не равен роли процесса или в `spec` есть неизвестное поле.
- Секреты остаются в env из Kubernetes Secret под текущими именами (`POSTGRES__DSN`, `DATABASE__URI`, `MONGODB__URI`, `VALKEY__URL`, `SPICEDB__PRESHARED_KEY`, `EVENT_STORE__SUBJECT_KEKS`, `EVENT_STORE__SUBJECT_REF_KEY`, `PROVIDER__WEBHOOK_SECRET`, `PROVIDER__STRIPE_API_KEY`); в `spec` полей для них нет.
- **BREAKING** OpenTelemetry настраивается секциями `spec.telemetry` (`endpoint`, `protocol`, `serviceName`, `resourceAttributes`), `spec.tracing.enabled`, `spec.metrics.enabled` и `spec.logging.level`, а не `OTEL_*` и `LOG__LEVEL`. Протокол OTLP — `grpc` или `http/protobuf`.
- Ключи `spec` — camelCase, как в ресурсах Kubernetes; `config.example.*` каждого сервиса переписываются в новый формат, delivery переходит с TOML на YAML.
- **BREAKING** Helm chart сервисов рендерят `config.yaml` в ConfigMap из блока values `config.spec` и структурных блоков, монтируют его и задают `CONFIG_FILE`. Блок values `env:` для несекретных настроек удаляется.

Не входит:

- CRD и оператор: файл не регистрируется в Kubernetes API, Helm остаётся способом развёртывания;
- BFF (Cosmo Router и его плагин) и UI;
- перечитывание файла без перезапуска процесса;
- публикация JSON Schema формата.

## Capabilities

### New Capabilities

Нет.

### Modified Capabilities

- `application-runtime`: формат и проверка файла конфигурации, источник секретов, настройка телеметрии из файла; сценарий fail fast проверяется на файле, а не на env.

## Impact

- `boundaries/orders`, `boundaries/controlplane` (Go, viper): `pkg/config`, `pkg/telemetry`, `di`, `cmd`, `config.example.yaml`.
- `boundaries/delivery` (Rust, `config`): `pkg/config`, `pkg/telemetry`, `config.example.toml` → `config.example.yaml`, feature `yaml` вместо `toml`.
- `boundaries/payments` (Python, pydantic-settings): `pkg/config`, `pkg/telemetry`, `di`; новая зависимость `pyyaml`.
- `boundaries/restaurants` (TypeScript, zod): `pkg/config`, `pkg/telemetry`, `cmd`; новая зависимость `yaml`.
- Helm chart всех пяти сервисов: `configmap.yaml`, `_helpers.tpl`, deployment, `values*.yaml`; `ops/kind/values/*.yaml` сервисов.
- `ops/runtime-contract`: процессы запускаются с файлом конфигурации.
- Makefile `run` сервисов.
- Документация: ADR 17, ADR 9, `docs/cloud-native-runtime.md`, `docs/observability.md`, README и `ops/README.md` сервисов, `ops/kind/README.md`.

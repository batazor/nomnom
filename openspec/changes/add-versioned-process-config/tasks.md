# Tasks

## 1. Загрузчик и телеметрия

- [x] 1.1 orders: `pkg/config` на `go.yaml.in/yaml/v3` с `KnownFields(true)`, проверка `apiVersion`, `kind: Orders`, `metadata.name` роли, секреты `POSTGRES__DSN` и `SPICEDB__PRESHARED_KEY` из env, viper удалён; `orders-admin` сверяет только `apiVersion` и `kind` (`FR-762`, `FR-763`, `FR-764`, `FR-766`).
- [x] 1.2 orders: `pkg/telemetry` и `di` настраивают SDK из `spec.telemetry`, `spec.tracing`, `spec.metrics`, `spec.logging`, `service.instance.id` из `POD_NAME`, без `OTEL_*` и `LOG__LEVEL` (`FR-765`, `FR-767`, `FR-768`).
- [x] 1.3 controlplane: то же для `kind: ControlPlane`, `CONFIG_FILE` становится обязательным (`FR-762`–`FR-768`).
- [x] 1.4 delivery: crate `config` с feature `yaml`, `deny_unknown_fields` и camelCase, секреты `DATABASE__URI`, `VALKEY__URL`, `SPICEDB__PRESHARED_KEY` из env; `delivery-admin` сверяет только `apiVersion` и `kind`; телеметрия из файла (`FR-762`–`FR-768`).
- [x] 1.5 payments: `yaml.safe_load` и pydantic-модели с `extra="forbid"` и camelCase-алиасами, секреты из env через `BaseSettings`; `payments_admin` сверяет только `apiVersion` и `kind`; телеметрия из файла; зависимость `pyyaml` (`FR-762`–`FR-768`).
- [x] 1.6 restaurants: пакет `yaml` и `z.strictObject`, секреты `MONGODB__URI`, `VALKEY__URL`, `SPICEDB__PRESHARED_KEY` из `process.env`; `restaurants-admin` сверяет только `apiVersion` и `kind`; телеметрия из файла (`FR-762`–`FR-768`).
- [x] 1.7 Unit-тесты загрузчика и телеметрии в каждом сервисе по сценариям `AC-1067`–`AC-1082`.
- [x] 1.8 `config.example.*` всех сервисов в новом формате (delivery: `config.example.toml` → `config.example.yaml`); `make run` задаёт `CONFIG_FILE` и `POD_NAME`.

## 2. Helm и kind

- [x] 2.1 Chart orders, delivery, payments, restaurants, controlplane: ConfigMap `config.yaml` из `config.spec` и полей chart (роль, пути JWKS и token dir, `service.version`), монтирование в `/etc/nomnom/config.yaml`, `CONFIG_FILE`, `POD_NAME`, аннотация `checksum/config`; ошибка `helm template` для ключа, которым владеет chart.
- [x] 2.2 Chart: удалить `env:` и рендер несекретных `SECTION__KEY`, `OTEL_*`, `LOG__LEVEL`; структурные блоки `spicedb`, `postgres`, `telemetry` и аналоги перенести в `config.spec`, на уровне chart оставить ссылки на Secret и ConfigMap JWKS; `values-<role>.yaml` переопределяют секции роли.
- [x] 2.3 `ops/kind/values/*.yaml` сервисов: `env:` и структурные блоки → `config.spec`, `tracing.enabled` и `metrics.enabled` для kind.

## 3. Runtime contract

- [x] 3.1 Удалить `ops/runtime-contract` и `make runtime-contract`; fail fast (`FR-762`, `FR-763`, «Fail fast при старте») покрыт unit-тестами загрузчиков.

## 4. Документация

- [x] 4.1 ADR 17: абзац о конфигурации — версионированный файл процесса, секреты в env, env несекретных настроек не читается.
- [x] 4.2 ADR 9: телеметрия из файла, флаги `tracing.enabled` и `metrics.enabled`, протокол OTLP.
- [x] 4.3 `docs/cloud-native-runtime.md`, `docs/observability.md`, README и `ops/README.md` сервисов, `ops/kind/README.md`.

## 5. Проверка

- [ ] 5.1 `make check` в каждом сервисе, `helm template` каждого chart с kind values, kind e2e; `openspec validate add-versioned-process-config --strict`.

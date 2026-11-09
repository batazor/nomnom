# Spec Delta

## ADDED Requirements

### Requirement: Версионированный файл конфигурации
**ID:** FR-762

Процесс SHALL читать несекретную конфигурацию из YAML-файла по пути из env `CONFIG_FILE`. Файл SHALL иметь вид `apiVersion: config.nomnom.dev/v1alpha1`, `kind: <сервис>`, `metadata.name: <роль процесса>`, `spec: {…}`; kind — `Orders`, `Delivery`, `Payments`, `Restaurants` или `ControlPlane`. Процесс MUST завершаться с non-zero exit до открытия портов, если файла нет, `apiVersion` или `kind` не совпадают с ожидаемыми или `metadata.name` не равен роли процесса.

#### Scenario: Файл своей роли
**ID:** AC-1067

- **WHEN** `orders-worker` запускается с файлом `apiVersion: config.nomnom.dev/v1alpha1`, `kind: Orders`, `metadata.name: orders-worker` и валидным `spec`
- **THEN** процесс переходит в ready

#### Scenario: Чужая версия или kind
**ID:** AC-1068

- **WHEN** `orders` запускается с файлом `apiVersion: config.nomnom.dev/v1beta1` или `kind: Delivery`
- **THEN** процесс завершается с non-zero exit, ошибка называет ожидаемые `apiVersion` и `kind`, технический порт не открывается

#### Scenario: Файл другой роли
**ID:** AC-1069

- **WHEN** `orders-consumer` запускается с файлом `metadata.name: orders-worker`
- **THEN** процесс завершается с non-zero exit, технический порт не открывается

#### Scenario: Нет файла
**ID:** AC-1071

- **WHEN** процесс запускается без `CONFIG_FILE` или с путём к несуществующему файлу
- **THEN** процесс завершается с non-zero exit, технический порт не открывается

### Requirement: Конфигурация операторских команд
**ID:** FR-766

Операторские команды (`orders-admin`, `delivery-admin`, `payments_admin`, `restaurants-admin`) SHALL читать файл pod, в котором запущены, и MUST сверять только `apiVersion` и `kind`, но не `metadata.name`.

#### Scenario: Операторская команда в pod другой роли
**ID:** AC-1070

- **GIVEN** в pod `orders-consumer` смонтирован файл `kind: Orders`, `metadata.name: orders-consumer`
- **WHEN** оператор запускает в нём `orders-admin`
- **THEN** команда принимает файл

### Requirement: Строгая схема spec
**ID:** FR-763

Ключи `spec` SHALL быть camelCase. Неизвестный ключ на любом уровне файла, включая верхний, MUST завершать процесс с non-zero exit до открытия портов, а ошибка MUST содержать путь ключа. Секции других ролей того же kind MUST приниматься. Переменные окружения `SECTION__KEY` для несекретных настроек MUST NOT влиять на конфигурацию.

#### Scenario: Опечатка в ключе
**ID:** AC-1072

- **WHEN** файл содержит `spec.shutdown.drainTimout: 20s`
- **THEN** процесс завершается с non-zero exit, ошибка содержит `spec.shutdown.drainTimout`

#### Scenario: Неизвестная секция верхнего уровня
**ID:** AC-1073

- **WHEN** файл содержит ключ `status` рядом со `spec`
- **THEN** процесс завершается с non-zero exit

#### Scenario: Env несекретной настройки не действует
**ID:** AC-1074

- **GIVEN** файл задаёт `spec.shutdown.drainTimeout: 20s`
- **WHEN** процесс запускается с `SHUTDOWN__DRAIN_TIMEOUT=0ms`
- **THEN** процесс переходит в ready и использует drain timeout `20s`

### Requirement: Секреты только из окружения
**ID:** FR-764

Секреты SHALL передаваться только через env под именами `POSTGRES__DSN`, `DATABASE__URI`, `MONGODB__URI`, `VALKEY__URL`, `SPICEDB__PRESHARED_KEY`, `EVENT_STORE__SUBJECT_KEKS`, `EVENT_STORE__SUBJECT_REF_KEY`, `PROVIDER__WEBHOOK_SECRET`, `PROVIDER__STRIPE_API_KEY` — тот набор, который нужен процессу. `spec` MUST NOT содержать полей для секретов, поэтому секрет в файле — неизвестный ключ. Пустой обязательный секрет MUST завершать процесс с non-zero exit.

#### Scenario: Секрет в файле
**ID:** AC-1075

- **WHEN** файл `orders` содержит `spec.postgres.dsn`
- **THEN** процесс завершается с non-zero exit, ошибка содержит `spec.postgres.dsn`

#### Scenario: Секрет из окружения
**ID:** AC-1076

- **WHEN** `orders` запускается с валидным файлом и пустым `POSTGRES__DSN`
- **THEN** процесс завершается с non-zero exit, технический порт не открывается

### Requirement: Телеметрия из файла конфигурации
**ID:** FR-765

Процесс SHALL настраивать OpenTelemetry из `spec.telemetry` (`endpoint`, `protocol`, `serviceName`, `resourceAttributes`), `spec.tracing`, `spec.metrics`, а уровень логов — из `spec.logging.level`. Endpoint, протокол, `service.name`, resource attributes, включение экспорта, интервал метрик и уровень логов MUST NOT браться из `OTEL_*` и `LOG__LEVEL`.

#### Scenario: Env OpenTelemetry не действует
**ID:** AC-1079

- **GIVEN** файл задаёт `telemetry.serviceName: orders-worker` и `tracing.enabled: true`
- **WHEN** процесс запускается с `OTEL_SERVICE_NAME=other` и `OTEL_EXPORTER_OTLP_ENDPOINT` другого коллектора
- **THEN** spans уходят на `telemetry.endpoint` с `service.name` `orders-worker`

### Requirement: Включение экспорта телеметрии
**ID:** FR-767

`telemetry.protocol` SHALL быть `grpc` или `http/protobuf`, по умолчанию `grpc`. Экспорт spans SHALL включаться только `tracing.enabled: true`, экспорт метрик — только `metrics.enabled: true`; оба флага по умолчанию `false`. Если включён хотя бы один из них, `telemetry.endpoint` MUST быть задан. Интервал экспорта метрик `metrics.exportInterval` SHALL быть больше нуля, по умолчанию `60s`.

#### Scenario: Экспорт включён без endpoint
**ID:** AC-1077

- **WHEN** файл задаёт `tracing.enabled: true` и пустой `telemetry.endpoint`
- **THEN** процесс завершается с non-zero exit

#### Scenario: Неизвестный протокол
**ID:** AC-1078

- **WHEN** файл задаёт `telemetry.protocol: http/json`
- **THEN** процесс завершается с non-zero exit, ошибка перечисляет `grpc` и `http/protobuf`

#### Scenario: Endpoint без флагов
**ID:** AC-1080

- **WHEN** файл задаёт `telemetry.endpoint`, а `tracing.enabled` и `metrics.enabled` — `false`
- **THEN** процесс переходит в ready и не экспортирует spans и метрики

### Requirement: Идентичность процесса в телеметрии
**ID:** FR-768

`service.instance.id` SHALL браться из env `POD_NAME`, который MUST быть задан. `telemetry.resourceAttributes` MUST NOT содержать `service.name` и `service.instance.id`.

#### Scenario: Переопределение идентичности процесса
**ID:** AC-1081

- **WHEN** `telemetry.resourceAttributes` содержит `service.name` или `service.instance.id`
- **THEN** процесс завершается с non-zero exit

#### Scenario: Нет имени pod
**ID:** AC-1082

- **WHEN** процесс запускается без `POD_NAME`
- **THEN** процесс завершается с non-zero exit

## MODIFIED Requirements

### Requirement: Fail fast при старте

Процесс SHALL завершаться с non-zero exit без перехода в ready при невалидной конфигурации или невозможности открыть порт.

#### Scenario: Невалидная конфигурация

- **WHEN** файл конфигурации задаёт `spec.shutdown.drainTimeout: 0ms`
- **THEN** процесс завершается с non-zero exit, технический порт не открывается

#### Scenario: Бизнес-порт занят

- **WHEN** бизнес-порт уже занят другим процессом
- **THEN** процесс завершается с non-zero exit

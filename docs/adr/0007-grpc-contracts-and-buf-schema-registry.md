# 7. gRPC Contracts and Buf Schema Registry

Date: 2026-10-24

## Status

Accepted

## Context

Bounded contexts BiteRoute нужны синхронные service-to-service API с явно принадлежащими владельцам контрактами и воспроизводимой генерацией кода для Go, TypeScript, Rust и Python. Контракт не должен заставлять один контекст импортировать доменную модель или исходный код другого контекста, а protobuf-типы не должны проникать в `domains` и `applications`.

Асинхронное взаимодействие уже определено через Apache Kafka в [ADR 4](0004-use-event-driven-architecture-with-apache-kafka.md). Структура слайса из [ADR 6](0006-ddd-structure-inside-vertical-slices.md) помещает транспортные адаптеры в `infrastructure/`. Это решение определяет только синхронные service-to-service контракты, их размещение, публикацию и потребление.

## Decision

### Транспорт и границы

Для синхронного взаимодействия между сервисами использовать gRPC и Protocol Buffers. Исходными `.proto` владеет bounded context, предоставляющий API. Владелец генерирует серверные stubs из локального модуля; потребитель получает схему только из Buf Schema Registry (BSR) организации [`nomnom`](https://buf.build/nomnom/repositories).

gRPC-адаптер преобразует transport messages в прикладные commands или queries и преобразует результат обратно. Типы Protobuf не выходят за пределы `infrastructure`: `domains`, `domains/<domain>/vo` и `applications` продолжают использовать собственные типы.

Внешние mobile/web-клиенты не используют внутренние gRPC/Connect API напрямую. Их единая прикладная точка входа, публичный GraphQL-контракт на WunderGraph Cosmo и правила потребления BSR-модулей определены в [локальном ADR 3 сервиса BFF](../../boundaries/bff/docs/adr/0003-use-wundergraph-cosmo-for-the-bff.md). Асинхронное взаимодействие продолжает следовать ADR 4 и использует Kafka.

### Размещение контрактов

Корнем Buf-модуля является `infrastructure/grpc` сервиса-владельца. В текущей структуре каждый bounded context является корнем соответствующего сервиса:

```text
boundaries/<service>/
  infrastructure/
    grpc/
      nomnom/
        <domain-service>/
          v1/
            <domain-service>.proto  # messages
            service.proto           # service + rpc
```

Например, API заказов располагается так:

```text
boundaries/orders/infrastructure/grpc/nomnom/orders/v1/orders.proto
boundaries/orders/infrastructure/grpc/nomnom/orders/v1/service.proto
```

```proto
syntax = "proto3";

package nomnom.orders.v1;
```

Префикс `nomnom.` обязателен: он исключает коллизии top-level модулей с доменными пакетами и сторонними схемами. Правило Buf `PACKAGE_DIRECTORY_MATCH` требует, чтобы каталог внутри корня модуля совпадал с package, поэтому каталог `nomnom/` является частью пути. Дублирование имени сервиса в `boundaries/orders/.../nomnom/orders/v1` ожидаемо и не должно устраняться. Отдельный корневой `contracts/` не создаётся: транспортный контракт и адаптер остаются рядом с владельцем, а доменная реализация — в `domains` соответствующего слайса.

| API | Корень модуля | Каталог в модуле | Package | BSR-модуль |
| --- | --- | --- | --- | --- |
| Orders | `boundaries/orders/infrastructure/grpc` | `nomnom/orders/v1/` | `nomnom.orders.v1` | `buf.build/nomnom/orders` |
| Restaurants | `boundaries/restaurants/infrastructure/grpc` | `nomnom/restaurants/v1/` | `nomnom.restaurants.v1` | `buf.build/nomnom/restaurants` |
| Delivery | `boundaries/delivery/infrastructure/grpc` | `nomnom/delivery/v1/` | `nomnom.delivery.v1` | `buf.build/nomnom/delivery` |
| Payments | `boundaries/payments/infrastructure/grpc` | `nomnom/payments/v1/` | `nomnom.payments.v1` | `buf.build/nomnom/payments` |

### Самодостаточные API и общие типы

Собственные модули `nomnom` не импортируют друг друга: например, `delivery` не зависит от `orders`. Каждый API самодостаточен и передаёт нужные идентификаторы и данные в собственном контракте.

Стандартные типы импортируются из `buf.build/googleapis/googleapis`, включая `google.type.Money`, `google.rpc.Status` и `google.rpc.ErrorInfo`, и из `buf.build/protocolbuffers/wellknowntypes`. Доменные value objects при этом не заменяются transport messages.

Модуль `buf.build/nomnom/common` для действительно общих интеграционных типов, например структурированного Address или пагинации, откладывается до появления подтверждённого совместного контракта. До этого осознанно допускается дублирование transport messages между API.

### Именование и совместимость

Версия API является частью package: `nomnom.<domain-service>.v1`. Каждый RPC использует отдельные именованные `<Rpc>Request` и `<Rpc>Response`. Service получает суффикс `Service`, например `OrdersService`.

Buf lint category `STANDARD` уже включает проверки `SERVICE_SUFFIX`, `RPC_REQUEST_STANDARD_NAME`, `RPC_RESPONSE_STANDARD_NAME` и совпадения каталога с package. Breaking policy — `FILE`. В пределах `v1` поля не удаляются и не переименовываются: устаревшее поле помечается `[deprecated = true]`. Несовместимое изменение получает новый package `nomnom.<domain-service>.v2`; опубликованный `v1` остаётся доступным до вывода из эксплуатации. Ревью отдельно проверяет семантическую совместимость поведения и значений полей, которую schema checks обнаружить не могут.

### Потребление и generated code

Потребитель указывает BSR-модуль в `inputs[].module` своего `buf.gen.yaml` и добавляет к имени модуля суффикс с конкретным BSR commit, например `buf.build/nomnom/orders:<commit>`. Подвижные labels не используются для пиннинга. Обновление контракта — явное изменение commit ref в PR.

Generated code коммитится в `infrastructure` потребителя. CI повторяет `buf generate` и проверяет чистый `git diff --exit-code`. У владельца `buf.gen.yaml` находится в его `infrastructure/grpc` и использует локальный модуль; у каждого потребителя есть собственный `buf.gen.yaml` рядом с адаптером. Единого корневого `buf.gen.yaml` нет, поскольку в формате v2 `plugins[].out` применяется ко всем `inputs` данного файла.

Remote plugins всегда закрепляются на версии:

| Язык | Plugins |
| --- | --- |
| Go | `buf.build/protocolbuffers/go:v1.36.12`, `buf.build/grpc/go:v1.6.2` |
| TypeScript | `buf.build/bufbuild/es:v2.16.0` (в v2 генерирует messages и service descriptors; Connect runtime подключается отдельно) |
| Rust | `buf.build/community/neoeinstein-prost:v0.5.0`, `buf.build/community/neoeinstein-tonic:v0.5.0` |
| Python | `buf.build/protocolbuffers/python:v36.2`, `buf.build/protocolbuffers/pyi:v36.2`, `buf.build/grpc/python:v1.84.1` |

TypeScript-потребители являются Node-клиентами или Connect-клиентами: браузер не поддерживает нативный gRPC transport. Конкретный runtime и transport внешнего gateway этим ADR не выбираются.

Managed mode включён. `go_package_prefix` задаётся в `buf.gen.yaml`, поэтому `option go_package` не копируется в каждый `.proto`.

### Ошибки

Адаптер переводит доменные и прикладные ошибки в фиксированные gRPC status codes. Структурированные детали передаются через `google.rpc.ErrorInfo`: `domain` содержит стабильное имя API, например `nomnom.orders.v1`, а `reason` — стабильный `UPPER_SNAKE_CASE` идентификатор причины. Текстовое сообщение предназначено для диагностики и не является машинным контрактом.

| Категория ошибки | gRPC code | Пример `ErrorInfo.reason` |
| --- | --- | --- |
| Сущность не найдена | `NOT_FOUND` | `ORDER_NOT_FOUND` |
| Запрос или поле некорректны | `INVALID_ARGUMENT` | `INVALID_DELIVERY_ADDRESS` |
| Текущее состояние запрещает операцию | `FAILED_PRECONDITION` | `ORDER_ALREADY_COMPLETED` |
| Ресурс с таким идентификатором уже существует | `ALREADY_EXISTS` | `PAYMENT_ALREADY_EXISTS` |
| Ошибка аутентификации | `UNAUTHENTICATED` | `AUTHENTICATION_REQUIRED` |
| Недостаточно прав | `PERMISSION_DENIED` | `OPERATION_NOT_ALLOWED` |
| Неожиданная внутренняя ошибка | `INTERNAL` | `INTERNAL_ERROR` |
| Зависимость временно недоступна | `UNAVAILABLE` | `DEPENDENCY_UNAVAILABLE` |

Конкретный адаптер расширяет таблицу стабильными причинами своего API, но не меняет значение общей категории. Неожиданные внутренние данные не раскрываются клиенту.

### Скелет корневого `buf.yaml`

На следующем шаге в корне репозитория создаётся конфигурация формата v2:

```yaml
version: v2
modules:
  - path: boundaries/orders/infrastructure/grpc
    name: buf.build/nomnom/orders
  - path: boundaries/restaurants/infrastructure/grpc
    name: buf.build/nomnom/restaurants
  - path: boundaries/delivery/infrastructure/grpc
    name: buf.build/nomnom/delivery
  - path: boundaries/payments/infrastructure/grpc
    name: buf.build/nomnom/payments
deps:
  - buf.build/googleapis/googleapis
  - buf.build/protocolbuffers/wellknowntypes
lint:
  use:
    - STANDARD
breaking:
  use:
    - FILE
```

Зависимости разрешаются в `buf.lock`. `buf dep update` выполняется осознанно в PR и не запускается автоматически в CI.

### Скелеты `buf.gen.yaml`

Владелец использует локальный input. Например, `boundaries/orders/infrastructure/grpc/buf.gen.yaml`:

```yaml
version: v2
managed:
  enabled: true
  override:
    - file_option: go_package_prefix
      value: nomnom/orders/infrastructure/grpc/gen
plugins:
  - remote: buf.build/protocolbuffers/go:v1.36.12
    out: gen
    opt: paths=source_relative
  - remote: buf.build/grpc/go:v1.6.2
    out: gen
    opt: paths=source_relative
inputs:
  - directory: .
```

Потребитель использует опубликованный модуль с конкретным commit ref. Пример Go-потребителя API заказов:

```yaml
version: v2
managed:
  enabled: true
  override:
    - file_option: go_package_prefix
      value: <consumer-go-module>/infrastructure/grpc/gen
plugins:
  - remote: buf.build/protocolbuffers/go:v1.36.12
    out: gen
    opt: paths=source_relative
  - remote: buf.build/grpc/go:v1.6.2
    out: gen
    opt: paths=source_relative
inputs:
  - module: buf.build/nomnom/orders:<pinned-bsr-commit>
```

Для остальных языков сохраняются те же `managed` и pinned `inputs`, а `plugins` заменяются целевой группой:

```yaml
# TypeScript
plugins:
  - remote: buf.build/bufbuild/es:v2.16.0
    out: gen

# Rust
plugins:
  - remote: buf.build/community/neoeinstein-prost:v0.5.0
    out: gen
  - remote: buf.build/community/neoeinstein-tonic:v0.5.0
    out: gen

# Python
plugins:
  - remote: buf.build/protocolbuffers/python:v36.2
    out: gen
  - remote: buf.build/protocolbuffers/pyi:v36.2
    out: gen
  - remote: buf.build/grpc/python:v1.84.1
    out: gen
```

Значения output directories и `go_package_prefix` уточняются для языка и layout конкретного потребителя, но локальный input владельца, BSR input потребителя, версии plugins и pinned commit не меняются произвольно.

### Проверки и публикация

В PR выполняются:

```sh
buf format --diff --exit-code                    # форматирование схем
buf lint                                         # правила STANDARD
buf build                                        # компилируемость workspace
buf breaking --against buf.build/nomnom/<module> # совместимость с последним push
buf generate                                     # stubs владельца или потребителя
git diff --exit-code                             # generated code актуален
```

Baseline для `buf breaking` — последний опубликованный commit соответствующего модуля в BSR. До первой публикации используется `.git#branch=main`. После успешных проверок push выполняется только из `main`:

```sh
buf push --label main
```

Label `main` соответствует ветке `main`, но потребители всё равно закрепляют commit, а не label. Авторизация выполняется через `BUF_TOKEN` из секретов CI или окружения.

## Consequences

Контракт имеет одного владельца, расположен рядом с адаптером и распространяется через BSR без импорта чужой доменной модели. Закреплённые schema commits и версии plugins делают генерацию воспроизводимой; `STANDARD` и `FILE` формализуют именование и защиту совместимости. Цена решения — committed generated code, явные PR для обновления refs, поддержка нескольких language-specific generator configs и временное дублирование общих transport messages.

Этот ADR документирует решение, но не добавляет `.proto`, Buf-конфигурации, generated code или runtime-зависимости. Следующие шаги: создать реальные `buf.yaml` и `buf.gen.yaml`, реализовать первые RPC и адаптеры, выполнить первую публикацию в BSR и оценить `buf.build/nomnom/common` после появления реального повторного использования. Внешняя клиентская граница отдельно определена в локальном ADR 3 сервиса BFF.

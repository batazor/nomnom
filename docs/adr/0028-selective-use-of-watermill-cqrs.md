# 28. Selective Use of Watermill CQRS

Date: 2026-10-25

## Status

Accepted

Связан с [4. Использовать Event-Driven Architecture и Apache Kafka](0004-use-event-driven-architecture-with-apache-kafka.md), [6. DDD Structure Inside Vertical Slices](0006-ddd-structure-inside-vertical-slices.md), [11. Transactional Inbox and Idempotent Event Processing](0011-transactional-inbox-and-idempotent-event-processing.md), [13. Transactional Outbox for Integration Event Publishing](0013-transactional-outbox-for-integration-event-publishing.md), [17. Cloud Native Application Runtime](0017-cloud-native-application-runtime.md), [20. Unit of Work for Local Transactions](0020-unit-of-work-for-local-transactions.md), [25. Integration Event Publication](0025-integration-event-publication.md)

Связан с ADR `orders` [3. Использовать Watermill для событийной интеграции](../../boundaries/orders/docs/adr/0003-use-watermill-for-event-integration-in-orders.md) и [4. Использование CQRS в orders](../../boundaries/orders/docs/adr/0004-использование-cqrs-в-orders.md)

## Context

Go-контекст `orders` использует Watermill для Kafka ([ADR orders 3](../../boundaries/orders/docs/adr/0003-use-watermill-for-event-integration-in-orders.md)) и CQRS на уровне application ([ADR orders 4](../../boundaries/orders/docs/adr/0004-использование-cqrs-в-orders.md)). Watermill предлагает собственный [компонент CQRS](https://watermill.io/docs/cqrs/): `CommandBus`, `CommandProcessor`, `EventBus`, `EventProcessor`, `EventGroupProcessor` и marshaler. Совпадение названий провоцирует ошибки:

- провести локальные Commands/Queries через `CommandBus` и Kafka, превратив синхронный use case в асинхронный;
- публиковать события через `EventBus.Publish()` мимо Outbox ([ADR 13](0013-transactional-outbox-for-integration-event-publishing.md));
- полагаться на дефолтный marshaler (имя Go-структуры), несовместимый с publisher'ами на Python, TypeScript и Rust;
- получить топик на каждый тип события (дефолт `EventProcessor`) и потерять порядок событий одного aggregate;
- превратить business error в бесконечный transport retry.

[ADR 6](0006-ddd-structure-inside-vertical-slices.md#входы-в-application) уже запрещает in-process command bus по умолчанию; adapter вызывает handler напрямую через DI.

На этом шаге фиксируются документация и решение. Код и зависимости не добавляются.

## Decision

### Application CQRS ≠ Watermill CommandBus

Локальный CQRS сохраняется: `Command/Query → Application Handler → Domain/Read Model`. Commands и Queries — локальные typed inputs, handler получают через DI. Они не проходят через Watermill. Цепочка `HTTP/gRPC → CommandBus → Kafka → CommandProcessor` для синхронных use cases запрещена.

### Watermill CQRS = typed event dispatch для Kafka integration

Из Watermill CQRS используется только `EventProcessor` (в форме handler groups) для входящих integration events:

```text
Kafka → Watermill Subscriber → EventProcessor → Watermill handler (infrastructure)
→ deserialize envelope/DTO → map to Command → Application Handler
→ Inbox (insert-first) → UoW → Domain/FSM → Outbox → commit → return nil → ack
```

- Ack — только после commit UoW.
- Watermill handler — infrastructure adapter: десериализация, mapping в Command, вызов handler'а, классификация ошибки. Domain logic в нём нет.
- Application Handler не зависит от Watermill. `domains` не знает Watermill, Kafka, `EventProcessor`, `EventBus`, `CommandBus` и `Router`.
- Inbox обязателен ([ADR 11](0011-transactional-inbox-and-idempotent-event-processing.md)); порядок insert-first — по [Unit of Work](../unit-of-work.md).

Другие языки используют свои consumer-реализации по тем же правилам: dispatch по `event_type`, Inbox, ack после commit, классификация ошибок.

### Marshaler

Используется custom `CommandEventMarshaler`:

- имя события — `event_type` Protobuf envelope (или header, по [ADR 25](0025-integration-event-publication.md)), то есть полное имя Protobuf-сообщения, например `nomnom.restaurants.v1.RestaurantOrderAccepted`;
- payload — Protobuf;
- `event_id` и trace context переносятся из envelope/headers в metadata сообщения.

Дефолтный marshaler по имени Go-структуры не используется: события публикуют контексты на других языках.

### Топики, ordering, handler groups

- Топик — stream aggregate контекста-источника, например `restaurants.v1.restaurant-order-events`. Ключ — `aggregate_id`.
- Типы событий внутри стрима маршрутизируются по `event_type`.
- Handlers одного стрима регистрируются одной handler group с общим subscriber'ом: так сохраняется порядок событий одного aggregate.
- Топик-на-тип-события (дефолт `EventProcessor`) не используется.
- Ordering ≠ idempotency. `aggregate_version` проверяет application, если сценарию нужен порядок.
- Конкретные имена топиков и partitioning — integration specification.

### Ошибки

| Класс | Источник | Действие handler'а |
| --- | --- | --- |
| Transient (DB, network) | infrastructure / application | nack → Retry middleware с backoff и лимитом |
| Deserialization, unknown `event_type`, permanent | infrastructure | poison topic (DLQ) + ack |
| Business error | domain / application | ack; результат — через outbox-событие или лог, не retry |
| Duplicate (Inbox) | application | ack, no-op |

Watermill не превращает business errors в бесконечный transport retry. Процедура восстановления из DLQ определяется отдельно.

### Outbox — единственный путь публикации

Исходящие события идут только по пути `Domain Event → Application → Transport DTO → Outbox → Watermill Forwarder → Kafka` ([ADR 13](0013-transactional-outbox-for-integration-event-publishing.md), [ADR 25](0025-integration-event-publication.md)). `EventBus.Publish()` до commit запрещён. `EventBus` не является абстракцией для Domain Events: они остаются in-memory. Второго пути публикации рядом с Outbox нет.

### CommandBus и Queries

`CommandBus` и `CommandProcessor` не используются: Commands — локальные typed inputs, синхронные запросы ждут результат, orchestration — Temporal, Kafka — интеграция между контекстами. Применение `CommandBus` для действительно асинхронных команд возможно позже отдельным решением.

Queries выполняются напрямую: `Query Handler → Read Repository/Projection → DTO`, не через Watermill.

### Размещение

```text
<slice>/
  applications/handler.*
  infrastructure/messaging/watermill_handler.*, dto.*, mapper.*
```

`Router`, subscriber, publisher, `EventProcessor`, регистрация handlers, middleware (retry, poison, OpenTelemetry, metrics), lifecycle и graceful shutdown — composition root deployable role consumer (`cmd/<boundary>-consumer`, по аналогии с `cmd/orders-worker`). Router внутри slice не создаётся.

### Observability

OpenTelemetry middleware восстанавливает trace context из headers. Метрики: processing duration, failures по классу ошибки, retry/redelivery, consumer lag, duplicates (Inbox), poison count. `event_id` и `aggregate_id` в labels не используются ([ADR 9](0009-использовать-opentelemetry-и-grafana-observability-stack.md)).

### Тестирование

- Unit — application handlers без Watermill.
- Wiring — Watermill `gochannel` Pub/Sub in-memory: marshaler, маршрутизация по `event_type`, handler groups, error mapping.
- Integration — Testcontainers: `Kafka → Watermill → EventProcessor → Inbox → handler`, ordering по ключу, retry и poison.
- E2E — kind ([ADR 14](0014-использовать-kind-для-тестового-окружения-kubernetes.md)).

## Consequences

Входящие integration events получают типизированный dispatch, единые middleware и одинаковое поведение ошибок без ручного switch по `event_type`. Граница между application CQRS и транспортом становится явной: use cases не меняют синхронность из-за выбора библиотеки, публикация остаётся атомарной с state.

Цена — custom marshaler и handler groups вместо дефолтов Watermill, обязательная классификация ошибок в каждом Watermill handler и отдельный процесс consumer на boundary. Business error подтверждается, поэтому application обязан явно зафиксировать результат (outbox-событие или лог), иначе факт отказа теряется. DLQ требует процедуры восстановления, которая пока не определена.

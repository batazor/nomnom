# CQRS и Watermill

Решение: [ADR 28. Selective Use of Watermill CQRS](adr/0028-selective-use-of-watermill-cqrs.md). Application CQRS `orders` — [ADR orders 4](../boundaries/orders/docs/adr/0004-использование-cqrs-в-orders.md).

## Application CQRS ≠ Watermill CommandBus

```text
gRPC/HTTP/Temporal Activity → Command/Query → Application Handler → Domain/Read Model
```

- Commands и Queries — локальные typed inputs. Adapter получает handler через DI и вызывает напрямую ([ADR 6](adr/0006-ddd-structure-inside-vertical-slices.md#входы-в-application)).
- `CommandBus`/`CommandProcessor` Watermill не используются. `HTTP/gRPC → CommandBus → Kafka → CommandProcessor` для синхронных use cases запрещён.
- Queries: `Query Handler → Read Repository/Projection → DTO`, без Watermill.
- Orchestration — Temporal, интеграция контекстов — Kafka.

## Watermill CQRS = typed event dispatch для Kafka integration

`EventProcessor` (handler groups) используется только для входящих integration events:

```text
Kafka → Watermill Subscriber → EventProcessor → Watermill handler (infrastructure)
→ deserialize envelope/DTO → map to Command → Application Handler
→ Inbox (insert-first) → UoW → Domain/FSM → Outbox → commit → return nil → ack
```

| Слой | Знает Watermill | Ответственность |
| --- | --- | --- |
| `domains/` | Нет | Инварианты, FSM, Domain Events |
| `applications/` | Нет | Command handler, Inbox, UoW, решение о публикации |
| `infrastructure/messaging/` | Да | Watermill handler, DTO, mapper, классификация ошибок |
| `cmd/<boundary>-consumer` | Да | Router, subscriber, `EventProcessor`, middleware, lifecycle |

Ack — только после commit UoW. Другие языки реализуют тот же контракт своими consumers.

### Marshaler

Custom `CommandEventMarshaler`: имя — `event_type` envelope (полное имя Protobuf-сообщения), payload — Protobuf, `event_id` и trace context — в metadata. Дефолтный marshaler по имени Go-структуры не используется.

### Топология стримов

Топик — stream aggregate контекста-источника, ключ — `aggregate_id`. Одна handler group на стрим с общим subscriber'ом сохраняет порядок событий aggregate. Топик-на-тип-события не используется. Имена ниже — иллюстрация; окончательные имена и partitioning задаёт integration specification.

| Consumer slice | Стрим (пример) | События → use case |
| --- | --- | --- |
| `orders/slices/order` | `restaurants.v1.restaurant-order-events` | `RestaurantOrderAccepted` → Confirm Order; `RestaurantOrderRejected` → Reject Order; `PreparationStarted` → Record Preparation Started; `RestaurantOrderReady` → Record Order Ready; `PreparationDelayed`, `PreparationFailed` → только workflow Signal |
| `orders/slices/order` | `delivery.v1.delivery-events` | `CourierAssignedToDelivery` → Record Courier Assignment; `DeliveryPickedUp` → Record Pickup; `DeliveryCompleted` → Record Delivery Completion; `DeliveryFailed` → Record Delivery Failure |

Источник списка — [orders README](../boundaries/orders/README.md). `PaymentAuthorized` и `PaymentFailed` `orders` из Kafka не читает: в порте `fulfillment` нет соответствующих Kind, workflow получает результат оплаты через Activity. Неизвестный `event_type` в стриме — poison.

### Реализация в `orders`

- Envelope — Kafka headers (`event_id`, `event_type`, `occurred_at`, `aggregate_*`, `traceparent`), payload — Protobuf. Контракты событий — `events.proto` в модулях publisher'ов (`nomnom.restaurants.v1`, `nomnom.delivery.v1`).
- Общий transport: [`pkg/messaging`](../boundaries/orders/pkg/messaging) — `EnvelopeMarshaler`, классы ошибок, `NewConsumerRouter` (poison → retry → reject unknown → `EventGroupProcessor`).
- Slice adapter: [`slices/order/infrastructure/messaging`](../boundaries/orders/slices/order/infrastructure/messaging/README.md).
- Composition root: `cmd/orders-consumer`, `di/consumer.go`.

### Ошибки

| Класс | Источник | Действие |
| --- | --- | --- |
| Deserialization (битый record, невалидный envelope) | subscriber unmarshaler | `<stream>.dlq` + ack до handler |
| Unknown `event_type`, permanent | infrastructure | `<stream>.dlq` + ack |
| Transient (DB, network) | infrastructure / application | Retry с backoff и лимитом; затем nack — partition ждёт повторной доставки |
| Business error | domain / application | ack; результат — outbox-событие или лог, не retry |
| Duplicate (Inbox) | application | ack, no-op |

Middleware снаружи внутрь: `Recoverer → OpenTelemetry/metrics → deserialization guard → Retry → dead letter → unknown event_type → handler`. Unmarshaler никогда не возвращает ошибку: в sarama это остановило бы partition. Подробности, Kafka-клиент (ClientID, initial offset, otelsarama, ключ `aggregate_id`) и метрики — [ADR 28](adr/0028-selective-use-of-watermill-cqrs.md).

## Публикация

Только `Domain Event → Application → Transport DTO → Outbox → Watermill Forwarder → Kafka` ([ADR 13](adr/0013-transactional-outbox-for-integration-event-publishing.md)). `EventBus.Publish()` до commit запрещён; `EventBus` не используется для Domain Events.

## Observability и тесты

См. [Событийное взаимодействие](event-driven-architecture.md#observability) и [Testing Strategy](testing.md#watermill-consumers).

## Источники

- [Watermill CQRS](https://watermill.io/docs/cqrs/)

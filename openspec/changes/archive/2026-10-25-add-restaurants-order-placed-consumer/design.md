# Design

## Context

- `orders` публикует `nomnom.orders.v1.OrderPlaced` в `orders.v1.order-events` с ключом `order_id` и envelope в Kafka headers (`event_id`, `event_type`, `occurred_at`, `aggregate_*`, `traceparent`).
- В `restaurants` Receive Order Request идемпотентен по `order_id`: повтор того же состава возвращает существующий заказ, конфликтующий — `AlreadyExistsError`.
- ADR 11 требует Inbox `(consumer, event_id)` в одной транзакции с изменением состояния; ADR 31 — `@confluentinc/kafka-javascript` и MongoDB replica set.

## Decisions

### 1. Тот же use case, второй вход

Consumer вызывает `ReceiveOrderRequestHandler` без изменений. RPC Temporal Activity и `OrderPlaced` создают один Restaurant Order: кто первый, тот создаёт `RECEIVED`, второй получает существующий. Use case требует `canCoordinateFulfillment`; в процессе consumer его реализует `OrdersEventSourceAccessControl`: разрешено только subject `service:orders`, остальные проверки — отказ. SpiceDB consumer не нужен: событие подлинно, потому что в topic пишет только Outbox `orders`.

### 2. Inbox

Документ `inbox`: `_id = {consumer}:{eventId}`, `consumer`, `eventId`, `eventType`, `aggregateId`, `topic`, `processedAt`. В транзакции: поиск по `(consumer, eventId)` → вставка → handler. Duplicate key вставки — duplicate; конкурентная транзакция с тем же `_id` получает WriteConflict, driver повторяет транзакцию и видит запись. Retention — TTL index 30 дней (дольше retention Kafka по умолчанию). Имя consumer — `restaurants.order-placed`.

### 3. Классы ошибок

| Класс | Примеры | Действие |
| --- | --- | --- |
| deserialization | нет `event_id`/`event_type`/`occurred_at`/`aggregate_id`, payload не Protobuf | `<stream>.dlq` + ack |
| permanent | пустой `order_id`/`restaurant_id`, нет `items`, невалидный состав или количество | `<stream>.dlq` + ack |
| business | ресторан не найден, конфликтующий состав уже полученного заказа | ack, log |
| transient | остальное (MongoDB) | повтор с backoff, затем ошибка → Kafka доставляет снова |

Dead letter — исходные key, value и headers плюс `reason_poisoned`, `topic_poisoned`, `consumer_group_poisoned` (как PoisonQueue Watermill в `orders`); DLQ стрима общий для всех consumers. Неизвестный `event_type` (`OrderCompleted`, `OrderCancelled`) подтверждается без обработки: стрим публикует и типы, которые этот consumer не потребляет. Ошибка публикации dead letter — transient.

### 4. Kafka и runtime

Consumer group `restaurants`, новая группа читает с `oldest`; offset сообщения сохраняется только после успешной обработки (ошибка `eachMessage` возвращает consumer к offset), коммит автоматический. `SIGTERM`: not ready → readiness delay → `disconnect`, который дожидается начатого сообщения; deadline drain прерывает его повторы, и сообщение доставляется снова.

## Risks / Trade-offs

- [Два входа Receive Order Request] → сходятся по `order_id`; при конфликте состава (не ожидается: оба из одного Order) consumer пишет бизнес-исход в log.
- [`OrderPlaced`, опубликованные до поля `items`] → уходят в DLQ; в kind данные эфемерны.
- [Без SpiceDB в consumer] → доверие к topic; в production — Kafka ACL на запись в `orders.v1.order-events` только `orders`.

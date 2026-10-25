# Proposal

## Why

Restaurant Order создаётся только RPC `ReceiveOrderRequest` из Temporal Activity `orders`, а этот вызов зависит от worker, internal gateway и service identity. `orders` уже публикует `OrderPlaced` через Outbox в `orders.v1.order-events`, но у `restaurants` нет ни одного Kafka consumer и Inbox (ADR 11 описывает их, ADR 31 выбирает клиент `@confluentinc/kafka-javascript`), а в `OrderPlaced` нет состава заказа — по нему нельзя создать Restaurant Order.

## What Changes

- **orders**: `OrderPlaced` получает `repeated OrderItem items = 6` (`menu_item_id`, `quantity`, `unit_price`); Domain Event и mapper Outbox передают состав заказа. Добавление поля обратно совместимо.
- **restaurants**:
  - Transactional Inbox в MongoDB: коллекция `inbox`, unique `{consumer, eventId}`, TTL 30 дней; Inbox и изменения handler — одна транзакция `MongoUnitOfWork`.
  - Обработка сообщения: envelope из Kafka headers, handler по `event_type`, классы ошибок deserialization/permanent → `<stream>.dlq`, business → ack, transient → повтор с backoff, затем повторная доставка Kafka; метрики inventory consumer.
  - Handler `OrderPlaced` → существующий use case Receive Order Request (subject `service:orders`, access control процесса разрешает только координацию fulfillment от источника стрима).
  - Процесс `cmd/restaurants-consumer` с runtime contract (health на техническом порту, drain по `SIGTERM`), fail fast без `MONGODB__URI` и `KAFKA__BROKERS`.
- **ops**: `values-consumer.yaml` chart `restaurants`, release `restaurants-consumer` в Skaffold и values kind; NetworkPolicy не меняется — pod `nomnom-restaurants` уже ходят в Kafka и MongoDB.

Не входит: consumer собственных событий `restaurants`, отказ Temporal Activity от RPC `ReceiveOrderRequest`, Kafka ACL на topic.

## Capabilities

### New Capabilities

Нет.

### Modified Capabilities

- `restaurant-order-lifecycle`: Receive Order Request по `OrderPlaced` через Inbox.
- `order-persistence`: `OrderPlaced` несёт состав заказа.
- `kubernetes-test-environment`: release `restaurants-consumer`.

## Impact

`boundaries/restaurants` (cmd, di, pkg/infrastructure/{kafka,messaging,mongodb,memory}, slice `restaurantorder`, config, chart, тесты), `boundaries/orders` (`events.proto`, Domain Event `OrderPlaced`, mapper Outbox, тесты), `ops/kind` (Skaffold, values), `docs/domain-events.md`.

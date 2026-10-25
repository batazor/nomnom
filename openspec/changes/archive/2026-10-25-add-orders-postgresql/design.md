# Design

## Decisions

### Схема

| Таблица | Содержимое |
| --- | --- |
| `orders` | `id`, `customer_id`, `restaurant_id`, `delivery_address`, `total_amount numeric`, `total_currency`, `state`, `placed_at`, `cancelled_at`, `negative_outcome`, `cancellation_code`, `version bigint`, `authorization_revision` |
| `order_items` | `order_id → orders`, `position`, `menu_item_id`, `quantity`, `unit_price_amount`, `unit_price_currency`; PK `(order_id, menu_item_id)` |
| `order_facts` | `order_id → orders`, `fact`, `occurred_at`, `reason`; PK `(order_id, fact)` |
| `inbox` | PK `(consumer, event_id)`, `received_at` |
| `idempotency_keys` | PK `(customer_id, key)`, `order_id` |
| `watermill_orders_outbox`, `watermill_offsets_orders_outbox` | DDL `watermill-sql` PostgreSQL schema/offsets adapter, создаётся миграцией |

Индекс `orders(customer_id, placed_at desc, id)` для `ListCustomerOrders`. Ссылок на чужие контексты нет (ADR 8).

### Unit of Work

`Do(ctx, fn)` открывает `READ COMMITTED` транзакцию и кладёт её в context; вложенный `Do` присоединяется к внешней. Repository, Inbox, Outbox и idempotency keys работают на транзакции из context, вне её — на пуле (queries). `Save`: `UPDATE … WHERE id = $1 AND version = $2`; 0 строк — `application.ErrConcurrencyConflict`. Panic откатывает транзакцию и пробрасывается.

### Outbox

`Append` переводит Domain Events в Integration Events `nomnom.orders.v1.*` (proto в `infrastructure/grpc/nomnom/orders/v1/events.proto`), строит envelope (`event_id` = UUIDv5 от `order:<id>:<version>:<index>`, `event_type`, `occurred_at`, `aggregate_*`, ключ `order_id`) и публикует через `forwarder.Publisher` поверх `watermill-sql` publisher на текущей транзакции. Forwarder (`cmd/orders-forwarder`) читает `orders_outbox` и пишет в Kafka `orders.v1.order-events`; запускается одним активным экземпляром (ADR 13).

### Authorization revision

`Relationships.GrantOrderCustomer` возвращает `application.AuthorizationRevision`; `placeorder` внутри UoW вызывает `Revisions.RememberRevision(orderID, revision)`. SpiceDB-адаптер получает `RevisionReader` и проверяет `order:<id>` с `at_least_as_fresh`, если revision есть.

### Процессы и конфигурация

`POSTGRES__DSN` (Secret), `POSTGRES__MAX_CONNS` (10). API, worker, consumer, forwarder требуют DSN — без него ошибка startup. `orders-migrate` применяет миграции и завершается; в Helm — init container каждого workload'а (goose берёт advisory lock).

### Проверка

Integration-тесты на PostgreSQL 18.6 в Docker (`make postgres-dev`, `POSTGRES_DSN`): контракт репозитория (round-trip, конфликт версии), UoW (commit, rollback, panic, nested), Inbox, idempotency, read models, Outbox + Forwarder в gochannel. Worker e2e против Temporal и PostgreSQL.

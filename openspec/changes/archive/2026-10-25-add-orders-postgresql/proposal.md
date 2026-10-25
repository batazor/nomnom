# Proposal

## Why

API, worker и consumer `orders` держат каждый свой in-memory store: worker не видит заказов API, consumer не видит заказов worker'а, а Outbox никуда не публикует. ADR 8, 13 и 20 выбирают PostgreSQL, Transactional Outbox через Watermill и Unit of Work, но реализации нет. ZedToken собственной записи в SpiceDB хранится только в памяти процесса.

## What Changes

- PostgreSQL 18 (pgx v5): миграции goose (embedded, advisory lock), процесс `cmd/orders-migrate`.
- Postgres Store в `slices/order/infrastructure/postgres`: `UnitOfWork` (`READ COMMITTED`, транзакция в context), `order.Repository` с optimistic concurrency по `version`, Inbox `(consumer, event_id)`, idempotency keys, read models `getorder`/`listcustomerorders`, authorization revision.
- Transactional Outbox: Domain Events → Integration Events `nomnom.orders.v1` (`OrderPlaced`, `OrderCompleted`, `OrderCancelled`) в той же транзакции через `watermill-sql` и Forwarder envelope; процесс `cmd/orders-forwarder` доставляет в Kafka `orders.v1.order-events`.
- Порт authorization revision: `placeorder` сохраняет ZedToken `TOUCH order#customer` в строке `Order`; SpiceDB-адаптер читает его для `at_least_as_fresh`.
- API, worker, consumer и forwarder используют общий store; `POSTGRES__DSN` обязателен.
- Helm (migrate init container, forwarder release) и kind (PostgreSQL).

## Capabilities

### New Capabilities

- `order-persistence`: хранение `Order`, транзакционная граница, Inbox/Outbox и read models `orders` в PostgreSQL.

### Modified Capabilities

Нет.

## Impact

`boundaries/orders` (новые зависимости pgx, goose, watermill-sql), proto `nomnom.orders.v1` events, Helm и kind. In-memory store остаётся для unit-тестов.

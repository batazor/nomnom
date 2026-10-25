# 1. Использовать PostgreSQL для хранилища payments

Date: 2026-10-25

## Status

Accepted

## Context

Общесистемный [ADR 8](../../../../docs/adr/0008-storage-strategy-postgresql-and-mongodb.md) допускает PostgreSQL и MongoDB, оставляя выбор контексту. Application-слой `payments` опирается на порты `UnitOfWork`, `PaymentRepository`, `PaymentOperationRepository` (только `get`, поиск по idempotency key, `add`, `save`), query-портах `PendingOperationQuery` и `PaymentOperationHistory`, `PaymentOutbox` и `ProviderMessageInbox`: одна локальная транзакция должна атомарно менять `Payment`, `Payment Operation`, записи Outbox ([ADR 13](../../../../docs/adr/0013-transactional-outbox-for-integration-event-publishing.md)) и Inbox ([ADR 11](../../../../docs/adr/0011-transactional-inbox-and-idempotent-event-processing.md)). Контракты требуют уникальности `order_id` и idempotency key, не более одной незавершённой операции на `Payment` и optimistic concurrency по `version`. Финансовые суммы хранятся точно.

## Decision

Хранить данные `payments` в PostgreSQL; драйвер — psycopg 3 с пулом `psycopg_pool`, синхронный, как handlers.

- Unit of Work — одна транзакция PostgreSQL на соединении, которое repositories получают через context variable; вложенный вызов присоединяется.
- Инварианты закреплены ограничениями схемы: `UNIQUE (order_id)`, `UNIQUE (idempotency_key)`, частичный уникальный индекс `(payment_id) WHERE state = 'pending'`; нарушения отображаются в доменные ошибки, insert выполняется в savepoint.
- Optimistic concurrency — `UPDATE … WHERE id = ? AND version = ?`.
- Суммы — `NUMERIC` без потери масштаба.
- `outbox` и `inbox` (`PRIMARY KEY (consumer, event_id)`) живут в той же базе и пишутся в той же транзакции. Формат записи Outbox, детерминированный `event_id` и отбор публикуемых событий mapper'ом — по [ADR 31](../../../../docs/adr/0031-outbox-storage-polling-relay-and-clients.md).
- Схема — forward-only SQL-миграции в репозитории, применяемые отдельным entrypoint под advisory lock.
- `DATABASE__URL` обязателен: пустое значение или недоступная база завершают процесс на старте.

## Consequences

Атомарность бизнес-изменения, Outbox и Inbox обеспечивает локальная транзакция без распределённых протоколов. Контекст получает зависимость от PostgreSQL и необходимость сопровождать миграции и роль базы в каждом окружении. Outbox publisher, читающий `outbox` по `position`, и Kafka consumers поверх `inbox` реализуются отдельно. Собственный простой migrator заменяется инструментом миграций, если понадобятся откаты или сложные сценарии.

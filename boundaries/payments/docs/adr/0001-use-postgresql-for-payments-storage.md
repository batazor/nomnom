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
- `payments_outbox` и `inbox` (`PRIMARY KEY (consumer, event_id)`) живут в той же базе и пишутся в той же транзакции. Детерминированный `event_id` и отбор публикуемых событий mapper'ом — по [ADR 31](../../../../docs/adr/0031-outbox-storage-polling-relay-and-clients.md); поля записи, publication `payments_outbox` и удаление записей старше часа в транзакции очередной записи — по [ADR 37](../../../../docs/adr/0037-outbox-delivery-via-debezium-cdc.md).
- Схема — forward-only SQL-миграции в репозитории, вшитые в образ; процесс `payments` применяет их сам при старте библиотекой yoyo-migrations (backend psycopg 3) под session advisory lock PostgreSQL (lock-строка yoyo не переживает убитый процесс), до открытия портов (отдельного шага миграций нет).
- Миграциями владеет владелец таблиц: общие таблицы (`payments_outbox`, `inbox`) — набор `infrastructure` (`infrastructure/repository/migrations`, рядом с их кодом `outbox.py` и `repositories.py`), таблицы slice — набор этого slice (`slices/payment/infrastructure/repository/migrations`: `payments`, `payment_operations`). Технология хранения — деталь реализации: каталоги и пакеты по ней не называются. Миграции slice меняют только его таблицы, внешних ключей между slices нет. У каждого набора своя нумерация и свои таблицы yoyo (`_yoyo_migration_<owner>`, `_yoyo_log_<owner>`, `_yoyo_version_<owner>`): журнал и версия внутренней схемы yoyo не делятся между migration tables. Процесс применяет явный упорядоченный список наборов из DI (`infrastructure` первым) одним runner'ом под одним advisory lock; автопоиска наборов нет, набор без миграций — ошибка старта.
- `DATABASE__URL` обязателен: пустое значение или недоступная база завершают процесс на старте.

## Consequences

Атомарность бизнес-изменения, Outbox и Inbox обеспечивает локальная транзакция без распределённых протоколов. Контекст получает зависимость от PostgreSQL и необходимость сопровождать миграции и роль базы в каждом окружении. Outbox публикует Debezium из WAL (ADR 37), поэтому базе нужен `wal_level = logical`, а слот репликации держит WAL, пока коннектор его не прочитал. Kafka consumers поверх `inbox` реализуются отдельно. Миграции ведёт yoyo-migrations вместо собственного migrator'а: SQL-файлы остаются как есть, ORM и SQLAlchemy не нужны. Разделение схемы по владельцам переписало историю миграций без совместимости с базами прежней единой истории: такие базы пересоздаются.

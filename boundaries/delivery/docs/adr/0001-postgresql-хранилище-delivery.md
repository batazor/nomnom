# 1. PostgreSQL как хранилище delivery

Date: 2026-10-25

## Status

Accepted

## Context

[ADR 8](../../../../docs/adr/0008-storage-strategy-postgresql-and-mongodb.md) не выбирал хранилище `delivery`; [ADR 31](../../../../docs/adr/0031-outbox-storage-polling-relay-and-clients.md) выбрал PostgreSQL, `sqlx`, `sqlx::migrate!`, процесс `delivery-migrate` и схему Outbox. До этого решения состояние `delivery` жило в памяти процесса (`infrastructure/memory`), поэтому терялось при рестарте, а конкурентные назначения курьеров и Outbox не были защищены.

Порты репозиториев, Outbox и `UnitOfWork` синхронные и не принимают handle транзакции, а use cases вызываются из async handlers tonic. Ревизии SpiceDB (`authorization_revision`, FR-447) хранятся вместе с `Courier` и `Delivery`.

## Decision

- Хранилище — отдельная база PostgreSQL контекста; адаптер `infrastructure/postgres::PostgresStore` закрывает все порты хранилища: репозитории `Courier`, `Delivery`, `DeliveryOffer`, Outbox, location store, read models, Unit of Work и чтение reconciliation.
- Клиент — `sqlx` (ADR 31). Store владеет небольшим tokio runtime пула; синхронные порты выполняют `block_on` через `block_in_place` на multi-thread runtime процесса (как SpiceDB-клиент), на current-thread runtime — в scoped thread.
- Unit of Work: соединение текущей транзакции хранится в thread-local слоте, привязанном к store; все записи репозиториев и Outbox внутри `run` идут в одну транзакцию (`BEGIN`/`COMMIT`/`ROLLBACK`). Ошибка ввода-вывода при `COMMIT` — `CommitOutcomeUnknown`.
- Optimistic concurrency: агрегаты несут технический `version` (как `authorization_revision`). `save` — `UPDATE … SET version = version + 1 WHERE id = $1 AND version = $2`; 0 строк при существующем id — `RepositoryError::ConcurrentModification` → `ConcurrentModification` application error → gRPC `ABORTED`. Запись revision reconciler'ом тоже увеличивает `version`.
- Outbox — таблица `delivery_outbox` по ADR 31 (`seq`, `event_id` UUIDv5, `payload` Protobuf, `published_at`, `attempts`, `last_error`), пишется в транзакции агрегата с `aggregate_version` только что записанной строки. Domain Event → Integration Event переводит внедряемый `OutboxMapper`; по умолчанию `NoIntegrationEvents` (ничего не публикуется) до появления Protobuf-мапперов и relay.
- Конфигурация — `DATABASE__URL` (ADR 31) и `DATABASE__MAX_CONNS`; пустой URL или недоступная база завершают startup процесса и `delivery-migrate`. Миграции — `sqlx::migrate!` в процессе `delivery-migrate` (init container), advisory lock сериализует реплики.

## Consequences

Состояние `delivery` переживает рестарты, конкурентные изменения отсекаются без блокировок, события фиксируются атомарно с изменением агрегата. Для запуска процесса нужна база и Secret с URL; in-memory варианта хранилища нет: unit- и integration-тесты идут в настоящий PostgreSQL, который поднимает testcontainers (нужен Docker). Репозитории содержат только `get`, поиск по уникальному натуральному ключу (`find_by_order`), `add` и `save`; выборки наборов агрегатов — query-порты слайсов (`OffersOfDelivery`, `ExpiredDeliveryOffers`, `WaitingDeliveries`), которые возвращают id или read model, а команда загружает каждый агрегат через репозиторий. Thread-local Unit of Work не поддерживает вложенные транзакции и требует, чтобы работа внутри `run` выполнялась синхронно на одном потоке.

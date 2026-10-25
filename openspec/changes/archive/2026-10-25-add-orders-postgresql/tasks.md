# Tasks

- [x] 1.1 Зависимости, конфигурация `POSTGRES__*`, миграции goose, `cmd/orders-migrate`.
- [x] 2.1 Postgres Store: UoW, repository, Inbox, idempotency, read models; контрактные тесты (AC-468, AC-469).
- [x] 2.2 Proto событий `nomnom.orders.v1`, Outbox mapper, Forwarder и `cmd/orders-forwarder` (AC-470).
- [x] 2.3 Authorization revision: порты, SpiceDB-адаптер, postgres (AC-472).
- [x] 3.1 DI API/worker/consumer на общем store; e2e worker на PostgreSQL (AC-471).
- [x] 3.2 Helm: DSN Secret, migrate init container, forwarder release; kind PostgreSQL.
- [x] 4.1 README, `make check`, `openspec validate --strict`.

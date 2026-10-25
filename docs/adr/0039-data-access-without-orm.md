# 39. Доступ к данным без ORM: драйвер, query builder и готовые мигрант-библиотеки

Date: 2026-10-26

## Status

Accepted

Закрывает открытый вопрос «драйверы, ORM/ODM и инструменты миграций» из
[8. Storage Strategy](0008-storage-strategy-postgresql-and-mongodb.md); уточняет
[38. Миграции схемы применяет сам процесс при старте](0038-schema-migrations-at-process-startup.md).

## Context

Хранилищами контекстов владеет `infrastructure` ([ADR 8](0008-storage-strategy-postgresql-and-mongodb.md)),
доменные модели не зависят от SQL, BSON и драйверов. На запросы опираются явный Unit of Work
([ADR 20](0020-unit-of-work-for-local-transactions.md)) с optimistic concurrency по `version`,
Inbox/Outbox в одной локальной транзакции ([ADR 11](0011-transactional-inbox-and-idempotent-event-processing.md),
[ADR 13](0013-transactional-outbox-for-integration-event-publishing.md)), таблицы Outbox для
Debezium ([ADR 37](0037-outbox-delivery-via-debezium-cdc.md)) и пулер в session mode. ORM
прячет транзакции, lazy loading и генерируемый SQL за своим unit of work и в четырёх языках
дал бы четыре разных модели. При этом SQL, собранный конкатенацией строк, и собственные
мигрант-фреймворки каждого контекста (таблицы версий, advisory lock, lease в MongoDB) —
код, который команда поддерживает сама, а ошибки в запросах ловят только интеграционные тесты.

## Decision

ORM и ODM не используются. Запросы пишутся на SQL/BSON в `infrastructure` контекста поверх
драйвера; собственные мигрант-фреймворки заменяются готовыми библиотеками.

| Контекст | Драйвер | Запросы | Миграции |
|---|---|---|---|
| `orders` (Go) | pgx v5 | query builder [squirrel](https://github.com/Masterminds/squirrel) (`$`-placeholders), выполнение через pgx | [golang-migrate](https://github.com/golang-migrate/migrate) (driver pgx/v5, source iofs, SQL-файлы встроены в бинарь) |
| `payments` (Python) | psycopg 3 + `psycopg_pool` | SQL и `psycopg.sql` | [yoyo-migrations](https://ollycope.com/software/yoyo/) (SQL-файлы в пакете, backend `postgresql+psycopg`) |
| `delivery` (Rust) | sqlx | `sqlx::query!` / `query_as!` / `query_scalar!`, проверка против схемы при компиляции; кеш `.sqlx/` в репозитории, сборка с `SQLX_OFFLINE=true` | `sqlx::migrate!` |
| `restaurants` (TS) | официальный `mongodb` | BSON-запросы драйвера | [migrate-mongo](https://github.com/seppevs/migrate-mongo) (changelog и lock библиотеки) |

- Блокировку одновременного старта нескольких pods ([ADR 38](0038-schema-migrations-at-process-startup.md))
  даёт сама библиотека миграций: advisory lock golang-migrate и sqlx, в `payments` — `pg_advisory_lock` вокруг yoyo (lock-строка
  yoyo после убитого процесса осталась бы и требовала ручного `yoyo break-lock`),
  lock-коллекция migrate-mongo. У migrate-mongo `up` при занятом lock отказывает, а не ждёт, и
  вставляет lock без атомарной проверки: `restaurants` делает вставку атомарной unique index'ом
  на lock-коллекции и повторяет `up`, пока lock занят; миграции идемпотентны
  ([локальный ADR 2](../../boundaries/restaurants/docs/adr/0002-mongodb-collections-and-document-boundaries.md#эволюция-схемы)).
- Advisory lock берётся до `lock_timeout`, ожидание другой реплики им не ограничено. Сами
  миграции идут с `lock_timeout = 5s`: во время rolling update `ALTER TABLE`, вставший в очередь
  за транзакцией живого трафика, задержал бы все запросы к таблице, поэтому он падает, транзакция
  миграции откатывается, pod повторяет её после рестарта.
- Каждый файл миграции — одна транзакция. golang-migrate помечает версию `dirty` перед файлом;
  `orders` при старте возвращает `dirty`-версию к предыдущей и применяет файл заново, а не
  требует ручного `migrate force`: упавший файл изменений не оставил.
- Миграции forward-only и обратно совместимы с предыдущей версией кода.
- Таблицы и коллекции версий — свои у каждой библиотеки; переход на неё рассчитан на новую базу,
  совместимость со старыми таблицами версий не поддерживается.

## Consequences

- Транзакционные границы, isolation level, `FOR UPDATE` и проверки `version` остаются явными в коде
  репозиториев.
- В `delivery` опечатка в SQL или несовпадение типов колонок — ошибка компиляции; изменение
  миграций или запросов требует обновить `.sqlx/` (`make sqlx-prepare`), иначе сборка падает.
- В `orders` SQL собирается builder'ом, а не конкатенацией; корректность запросов по-прежнему
  проверяют контрактные тесты на testcontainers.
- Своих мигрант-фреймворков в контекстах нет; их тесты заменены проверкой поведения при старте.
- Существующие базы тестового окружения пересоздаются при переходе.

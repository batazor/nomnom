# 20. Unit of Work for Local Transactions

Date: 2026-10-24

## Status

Accepted

Связан с [6. DDD Structure Inside Vertical Slices](0006-ddd-structure-inside-vertical-slices.md) и [8. Storage Strategy: PostgreSQL and MongoDB](0008-storage-strategy-postgresql-and-mongodb.md)

Связан с [11. Transactional Inbox and Idempotent Event Processing](0011-transactional-inbox-and-idempotent-event-processing.md) и [13. Transactional Outbox for Integration Event Publishing](0013-transactional-outbox-for-integration-event-publishing.md)

Связан с [18. SpiceDB for Authorization](0018-spicedb-for-authorization.md)

## Context

[ADR 6](0006-ddd-structure-inside-vertical-slices.md) отдаёт `applications/` определение границы транзакции, но не задаёт её форму. [ADR 11](0011-transactional-inbox-and-idempotent-event-processing.md) и [ADR 13](0013-transactional-outbox-for-integration-event-publishing.md) требуют фиксировать Inbox, business state и Outbox в одной локальной транзакции хранилища boundary, а [ADR 8](0008-storage-strategy-postgresql-and-mongodb.md) оставляет открытыми транзакционные границы, isolation level и контроль конкуренции. В [документации событийного взаимодействия](../event-driven-architecture.md#открытые-вопросы-реализации) передача транзакции в репозитории и Outbox через unit of work указана как открытый вопрос.

Без общего решения каждый слайс будет открывать транзакции по-своему: в transport middleware на каждый request, внутри repositories, через DI-объект, раздающий repositories, или с внешними вызовами внутри транзакции. Это приводит к длинным locks, к внешним эффектам, которые не откатываются, к невозможности безопасно повторить транзакцию и к зависимости application- и domain-кода от драйверов.

Distributed transaction между хранилищами, Kafka, Temporal, SpiceDB и другими boundaries в архитектуре нет и не вводится.

На этом шаге фиксируется только архитектурное решение. Интерфейсы, реализации, миграции и тесты не создаются.

## Decision

Использовать Unit of Work (UoW) как явную локальную транзакционную границу application use case, которому нужна атомарность нескольких persistence operations в хранилище своего bounded context. Подробные правила — в [документации Unit of Work](../unit-of-work.md).

### Граница

- UoW охватывает только локальное хранилище одного boundary. Distributed transactions не используются.
- Kafka, Temporal, SpiceDB, gRPC к другим boundaries и internal gateway, HTTP и платёжные провайдеры находятся вне UoW.
- Границу транзакции определяет use case, а не transport: middleware «каждый gRPC request в одной DB transaction» запрещён.

### Размещение и зависимости

- Port — один на boundary, в shared application-уровне boundary: интерфейс из одного метода с семантикой `begin → execute → commit | rollback`. В Go — `Do(ctx, func(ctx) error) error`, в других языках — идиоматичный transaction scope.
- Реализация — одна на хранилище boundary, в shared infrastructure boundary, общая для слайсов. Она владеет механизмом propagation транзакции.
- Слайс зависит от UoW, только если сценарию нужна атомарность нескольких persistence operations.
- `domains` не знает о транзакциях, UoW, SQL, MongoDB sessions, Kafka, Temporal и gRPC.

### Не Service Locator

UoW не раздаёт repositories (`uow.Orders()`, `uow.Payments()`). Repositories, ports и handlers получают зависимости через DI ([ADR 5](0005-dependency-injection-strategy.md)): `Handler ← OrderRepository, Outbox, UnitOfWork`. Repositories, Inbox и Outbox участвуют в активной транзакции через единый transaction-scoped context/session, без driver types в application API. Внутри UoW repository не открывает и не коммитит свою транзакцию; вне UoW выполняет одиночный statement в autocommit.

Nested transactions не используются. Повторный вход в UoW при активной транзакции участвует в ней; commit, rollback и retry выполняет только внешний scope. Savepoints — отдельное решение.

### Правила внутри транзакции

- Внутри транзакции нет внешних вызовов: closure может быть повторён, locks должны быть короткими, внешний вызов не откатывается.
- Нет ожиданий, таймеров и user I/O.
- Идентификаторы aggregates (UUIDv7/ULID) генерируются в application до транзакции, чтобы внешние действия — SpiceDB relationship, idempotency key, Workflow ID — выполнялись до UoW с тем же id.

### Isolation, конкурентность и retry

- Isolation по умолчанию — `READ COMMITTED` в PostgreSQL. `REPEATABLE READ`/`SERIALIZABLE` — только по явной причине в документации use case.
- Конкурентные изменения aggregate контролируются optimistic concurrency по `version`; конфликт — типизированная application error. `SELECT … FOR UPDATE` — осознанно, для высокой конкуренции по одной строке.
- Retry транзакции bounded (ограниченное число попыток, backoff, deadline) и только на serialization failure, deadlock, transient transaction error и конфликт версии, загруженной внутри транзакции. Retries видны в logs, metrics и traces.

### Inbox и Outbox

- Outbox пишется в той же транзакции, что и state. В `orders` publisher `watermill-sql` исполняется на executor текущей транзакции; конкретный способ выбирается при реализации. Kafka publish — после commit Outbox publisher'ом.
- Inbox — insert-first: вставка `(consumer, event_id)` первой операцией, конфликт → rollback и no-op; уникальность обеспечивает storage engine. Порядок не противоречит ADR 11, который требует только, чтобы state и Outbox не фиксировались без записи Inbox.
- UoW охватывает Inbox, state и Outbox одной обработки, обеспечивая effectively-once в пределах хранилища consumer.

### Ошибки

Успех → commit; application, domain и infrastructure errors → rollback; panic/exception → rollback и propagate. Ошибка commit не является успехом. Неизвестный результат commit UoW автоматически не повторяет: он разрешается идемпотентностью операции — Inbox, idempotency key или ожидаемой `version`.

### Temporal и authorization

- Workflow не владеет транзакцией. Каждая Activity вызывает handler со своим UoW; Activities не объединяются одной транзакцией. Атомарность процесса — локальные транзакции, durable orchestration и compensation. Повтор Activity после commit не создаёт второй логический outbox event ([ADR 13](0013-transactional-outbox-for-integration-event-publishing.md#стабильность-event_id)).
- Порядок защищённой операции: `authorize → [SpiceDB relationship write при создании, с заранее сгенерированным id] → UoW → domain operation`. ZedToken сохраняется внутри UoW. Окно между check и commit допустимо; значимая race закрывается доменными инвариантами или отдельной consistency strategy.

### Хранилища

- `orders` / PostgreSQL — UoW применяется активно: state + Outbox, Inbox + state (+ Outbox), optimistic concurrency, idempotent commands. CQRS queries выполняются без UoW.
- `restaurants` / MongoDB — single-document atomicity по умолчанию; UoW с MongoDB transaction только для multi-document изменений или state + Inbox/Outbox, при реальной необходимости.
- `payments` — локально payment state + operation record + Outbox; provider call вне транзакции.
- `delivery` — transaction capabilities, atomic state + Inbox/Outbox и защита конкурентных courier assignments проверяются при выборе хранилища.

## Consequences

Атомарность state, Inbox, Outbox и idempotency-записей получает одно явное место — use case — и одинаковую форму во всех boundaries. Handlers остаются тестируемыми с test UoW, домен — без транзакций. Запрет внешних вызовов внутри транзакции делает её короткой и безопасно повторяемой, а bounded retry скрывает serialization failures, deadlocks и version conflicts от вызывающих.

Внешние действия выносятся до или после транзакции, поэтому каждый use case с такими действиями обязан явно описать порядок и идемпотентность: orphan SpiceDB relationships при сбое commit, повтор Signal Temporal после commit в Inbox-сценариях, вызов платёжного провайдера между двумя локальными транзакциями. Неизвестный результат commit требует идемпотентности каждой повторяемой операции.

Генерация id в application исключает идентификаторы, создаваемые базой, для aggregates. Механизм propagation транзакции и его интеграция с Watermill, MongoDB session и DI-контейнерами требуют реализации и integration/fault tests с реальными хранилищами, включая replica set MongoDB.

Storage-specific часть опирается на ADR 8 в статусе `Proposed`: при изменении выбора хранилищ она пересматривается. Для `payments` и `delivery` правила применяются после выбора хранилищ.

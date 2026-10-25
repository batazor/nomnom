# Unit of Work

Документ описывает Unit of Work (UoW) — локальную транзакционную границу application use case внутри bounded context. Решение зафиксировано в [ADR 20](adr/0020-unit-of-work-for-local-transactions.md). Документ дополняет [Inbox ADR](adr/0011-transactional-inbox-and-idempotent-event-processing.md), [Outbox ADR](adr/0013-transactional-outbox-for-integration-event-publishing.md), [DDD-структуру слайсов](adr/0006-ddd-structure-inside-vertical-slices.md), [SpiceDB ADR](adr/0018-spicedb-for-authorization.md) и [документацию событийного взаимодействия](event-driven-architecture.md).

Документ фиксирует правила. Port UoW и его in-memory реализации уже созданы вместе с application-слоем boundaries; production-реализации поверх хранилищ и миграции появятся с соответствующими SDD changes.

## Назначение

UoW нужен, когда один application use case должен атомарно сохранить несколько связанных изменений в локальном хранилище своего bounded context:

```text
load aggregate → apply domain behavior → persist aggregate → persist Inbox → persist Outbox → commit
```

Все шаги внутри — операции одного хранилища одного boundary. Либо фиксируются все изменения, либо ни одного.

UoW не является distributed transaction. Kafka, Temporal, SpiceDB, gRPC-вызовы других boundaries, internal gateway, HTTP и платёжные провайдеры в него не входят и не могут быть «откатаны» вместе с ним. Согласованность между контекстами обеспечивается Outbox, Inbox, идемпотентностью и durable orchestration ([ADR 8](adr/0008-storage-strategy-postgresql-and-mongodb.md), [ADR 11](adr/0011-transactional-inbox-and-idempotent-event-processing.md), [ADR 13](adr/0013-transactional-outbox-for-integration-event-publishing.md)).

### Когда UoW нужен, а когда нет

| Сценарий | UoW |
| --- | --- |
| Aggregate + Outbox | Да |
| Inbox + aggregate (+ Outbox) | Да |
| Aggregate + idempotency key команды и сохранённый результат в отдельной записи | Да |
| Несколько aggregates, связанных инвариантом, который нельзя выразить иначе (см. [`delivery`](#delivery)) | Да, с обоснованием в документации use case |
| Одиночное изменение одного aggregate одним statement/документом | Нет: autocommit |
| Query / read model (CQRS) | Нет |
| Temporal workflow | Нет: workflow не владеет транзакцией |

По умолчанию одна транзакция изменяет один aggregate плюс технические записи Inbox, Outbox и idempotency. Изменение нескольких aggregates в одной транзакции — осознанное исключение, а не способ обойти моделирование. Каждое такое исключение записывается с причиной; действующие исключения — [offer, `Delivery` и `Courier` в `delivery`](#исключение-offer-delivery-и-courier-в-одной-транзакции).

## Размещение

UoW — application port. Он нужен нескольким слайсам одного boundary с одинаковой формой, поэтому, в отличие от портов конкретных интеграций ([ADR 6](adr/0006-ddd-structure-inside-vertical-slices.md#направление-зависимостей)), объявляется один раз в shared application-уровне boundary, а не дублируется по слайсам:

| Boundary | Port (shared application-уровень) | Реализация (shared infrastructure boundary) |
| --- | --- | --- |
| `orders` (Go) | `pkg/application` | `slices/<slice>/infrastructure/postgres` |
| `restaurants` (TypeScript) | `pkg/application` | `slices/<slice>/infrastructure/mongodb` |
| `payments` (Python) | `pkg/application` | После выбора хранилища |
| `delivery` (Rust) | `pkg/application` | После выбора хранилища |

Пути реализации задаются относительно корня boundary так же, как `infrastructure/grpc` и `infrastructure/temporal`. Port и реализация появляются вместе с первым слайсом, которому нужна атомарность нескольких persistence operations; заранее не создаются.

В `payments` port реализован в `pkg/application/unit_of_work.py` как синхронный `UnitOfWork.run(work)`: runtime синхронный, transport и Temporal worker для Python не выбраны; переход на async меняет только сигнатуры ([design.md change `implement-payments-application-layer`](../openspec/changes/implement-payments-application-layer/design.md)).

Port — интерфейс из одного метода. Реализация одна на хранилище boundary и общая для всех слайсов: она же владеет механизмом propagation транзакции, которым пользуются репозитории, Inbox и Outbox этого boundary. Сборка — в composition root каждого процесса (API, consumer, Temporal worker) по [ADR 5](adr/0005-dependency-injection-strategy.md).

Слайс зависит от UoW, только если его сценарию нужна атомарность нескольких persistence operations. Handler, которому достаточно одного save, UoW не получает.

`domains/` и `domains/<domain>/vo/` не знают о транзакциях, UoW, SQL, MongoDB sessions, Kafka, Temporal и gRPC. Доменная модель не открывает, не коммитит и не ожидает транзакцию.

## Контракт

```text
begin → execute application operation → commit
                                      ↘ rollback on failure
```

Форма в Go:

```go
// pkg/application
type UnitOfWork interface {
	Do(ctx context.Context, fn func(ctx context.Context) error) error
}
```

Другие языки используют идиоматичный transaction scope с той же семантикой: callback `run(work)` или async context manager. Пример формы, а не утверждённый API:

| Язык | Форма |
| --- | --- |
| Go | `Do(ctx, func(ctx) error) error` |
| TypeScript | `run<T>(work: () => Promise<T>): Promise<T>` |
| Python | `async with unit_of_work():` или `await uow.run(work)` |
| Rust | `async fn run<T, F>(&self, work: F) -> Result<T, E>` по trait boundary |

Результат use case возвращается из scope (или записывается в переменную замыкания в Go), но не означает успех до commit.

### Пример handler'а

```go
type Handler struct {
	orders OrderRepository // контракт из domains
	outbox Outbox          // порт applications
	uow    UnitOfWork      // shared application port
}

func (h Handler) Handle(ctx context.Context, cmd CompleteOrder) (Result, error) {
	var res Result
	err := h.uow.Do(ctx, func(ctx context.Context) error {
		order, err := h.orders.Get(ctx, cmd.OrderID)
		if err != nil {
			return err
		}
		if err := order.Complete(); err != nil {
			return err
		}
		if err := h.orders.Save(ctx, order); err != nil {
			return err
		}
		res = Result{Version: order.Version()}
		return h.outbox.Add(ctx, OrderCompleted{OrderID: order.ID()})
	})
	return res, err
}
```

## Правила внутри транзакции

### Только локальное хранилище

Внутри `Do` выполняются только операции с хранилищем своего boundary. Запрещены:

- gRPC к другим boundaries и через internal gateway;
- SpiceDB (`CheckPermission`, `WriteRelationships`, lookup);
- Temporal client: start workflow, Signal, Update, Query;
- HTTP и платёжные провайдеры;
- Kafka produce и commit offset.

Причины:

1. `fn` может быть выполнен повторно (см. [retry](#retry-policy)) — внешний вызов повторится.
2. Locks и соединение удерживаются, пока идёт внешний вызов: время транзакции становится зависимым от чужой latency.
3. Внешний вызов не откатывается вместе с транзакцией: rollback оставит его эффект.

Внешние действия выполняются до `Do` или после успешного commit, а надёжная доставка эффекта после commit — через Outbox.

### Короткая транзакция

Внутри `Do` нет ожиданий, таймеров, sleep, user I/O, чтения тела потока или длинных вычислений. Валидация Command, загрузка справочных данных из других boundaries и подготовка входа выполняются до транзакции. `ctx` с deadline передаётся в scope; отмена `ctx` приводит к rollback.

### Идентификаторы генерируются до транзакции

Идентификаторы новых aggregates (UUIDv7 или ULID) генерирует application до `Do`. Это позволяет до транзакции выполнить действия, которым нужен id: записать creation relationship в SpiceDB, сформировать idempotency key, Workflow ID. Retry `fn` использует тот же id. Генерация id в базе (`serial`, `ObjectId` по умолчанию) для aggregates не используется.

`event_id` записи Outbox вычисляется детерминированно при создании записи внутри транзакции по [ADR 13](adr/0013-transactional-outbox-for-integration-event-publishing.md#стабильность-event_id); повтор `fn` после rollback вычисляет его заново от `aggregate_version`, загруженной в этой попытке, а прежняя запись не была зафиксирована.

## Isolation и конкурентность

### Isolation level

Дефолт PostgreSQL — `READ COMMITTED`. `REPEATABLE READ` и `SERIALIZABLE` выбираются только по явной причине, записанной в документации use case (например, инвариант по нескольким строкам, который нельзя защитить constraint'ом или версией). Повышенный уровень приносит serialization failures, поэтому такой use case обязан поддерживать retry.

Для MongoDB используются read concern `snapshot` и write concern `majority` в транзакциях; детали фиксируются при реализации `restaurants`.

### Optimistic concurrency

Конкурентные изменения aggregate контролируются optimistic concurrency по `version` aggregate:

- aggregate загружается с текущей `version`;
- repository `Save` записывает изменения с условием на загруженную версию (`UPDATE … WHERE id = $1 AND version = $2`, `updateOne({_id, version})`) и увеличивает её;
- ноль затронутых строк/документов → типизированная application error конфликта версии (например, `ErrConcurrencyConflict`), транзакция откатывается;
- infrastructure сопоставляет её с transport-кодом по [ADR 7](adr/0007-grpc-contracts-and-buf-schema-registry.md); конкретный код и `ErrorInfo.reason` для конфликта версии выбираются при реализации adapter'а.

`version` — та же `aggregate_version`, которая попадает в запись Outbox ([ADR 13](adr/0013-transactional-outbox-for-integration-event-publishing.md#запись-outbox)) и может использоваться как ожидаемая версия для идемпотентности Temporal Activity.

### Pessimistic locking

`SELECT … FOR UPDATE` (и аналоги) — осознанный выбор для сценариев с высокой конкуренцией по одной строке, где optimistic conflicts будут частыми (например, резервирование одного курьера несколькими offers). Выбор фиксируется в документации use case. Порядок блокировок нескольких строк детерминирован, чтобы не создавать deadlocks.

## Retry policy

Retry транзакции — bounded: ограниченное число попыток (по умолчанию 3) с exponential backoff и jitter, в пределах deadline `ctx`. Повторяется вся транзакция целиком: новый `begin`, повторное выполнение `fn`, commit.

Повторяются только:

| Причина | Пример |
| --- | --- |
| Serialization failure | PostgreSQL SQLSTATE `40001` |
| Deadlock | PostgreSQL SQLSTATE `40P01` |
| Transient transaction error | MongoDB label `TransientTransactionError` |
| Конфликт версии aggregate | `ErrConcurrencyConflict`, если версия загружена внутри `fn` |

Retry возможен только потому, что `fn` не имеет side effects вне хранилища. Если ожидаемая версия пришла от вызывающей стороны (Command из workflow, If-Match от клиента), конфликт версии не повторяется: он означает, что предположение вызывающего устарело, и возвращается ему.

Не повторяются: доменные и application errors, constraint violations (кроме обработанного Inbox-конфликта), ошибки валидации, отмена `ctx`, неизвестный результат commit.

Встроенные механизмы драйверов приводятся к этой политике. Например, `withTransaction` MongoDB повторяет callback до 120 секунд; число попыток и время ограничиваются так же, как в PostgreSQL.

Каждый retry логируется и отражается в metrics и traces с причиной (см. [Observability](#observability)).

## Repositories и propagation

Repository, Inbox и Outbox работают в контексте активной транзакции через единый механизм propagation текущего boundary:

| Boundary | Механизм propagation |
| --- | --- |
| `orders` | Транзакция хранится в `context.Context`; repository получает executor из `ctx`: активную транзакцию или pool |
| `restaurants` | `ClientSession` в `AsyncLocalStorage` или в message/request scope Awilix |
| `payments` | `contextvars` или scope Dependency Injector после выбора хранилища |
| `delivery` | Выбирается после выбора хранилища; application API не зависит от типов драйвера. Пока propagation закрыт in-memory адаптером `infrastructure/memory` с синхронным `run` ([design.md change `implement-delivery-application-layer`](../openspec/changes/implement-delivery-application-layer/design.md)) |

Правила:

- Application API (port UoW, контракты repositories, порт Outbox) не содержит driver types: `*sql.Tx`, `pgx.Tx`, `ClientSession`, connection.
- Внутри UoW repository не открывает, не коммитит и не откатывает собственную транзакцию.
- Вне UoW repository выполняет одиночный statement в autocommit. Это допустимо для сценариев без требования атомарности нескольких операций.
- Механизм propagation принадлежит реализации UoW в shared infrastructure boundary; repositories слайсов используют его, а не изобретают свой.

### Nested scopes

Nested transactions не предполагаются. Внутренние компоненты не открывают независимые транзакции внутри UoW. Savepoints — отдельное решение.

Повторный вызов `Do` при уже активной транзакции в `ctx` не создаёт новую транзакцию: он участвует в существующей, не коммитит её и не выполняет собственный retry. Commit, rollback и retry выполняет только внешний scope; ошибка внутреннего `fn` приводит к откату всей транзакции. Это нужно для Inbox: инфраструктурная обёртка consumer'а открывает scope и записывает Inbox, а handler слайса вызывает свой `Do` внутри него ([event-driven architecture](event-driven-architecture.md#размещение-в-коде)).

## Outbox и Inbox

### Outbox

Outbox записывается в той же транзакции, что и state:

```text
BEGIN
  update state
  insert outbox
COMMIT
```

Application решает, какое integration event создать, и передаёт его в порт Outbox; infrastructure преобразует его в Protobuf payload и пишет запись в текущую транзакцию ([ADR 6](adr/0006-ddd-structure-inside-vertical-slices.md#mapping)).

Для Watermill в `orders`: publisher `watermill-sql` исполняется на executor текущей транзакции — создаётся на транзакции, извлечённой из `ctx`, по одному на транзакцию ([ADR 13](adr/0013-transactional-outbox-for-integration-event-publishing.md#orders-go-postgresql-и-watermill)). Способ получения executor'а (`*sql.Tx` или адаптер pgx) выбирается при реализации; требование — Outbox и state в одной транзакции.

Kafka publish выполняется после commit Outbox publisher'ом (Watermill Forwarder в `orders`). Kafka в UoW не входит.

### Inbox

Inbox использует порядок insert-first:

```text
BEGIN
  insert inbox (consumer, event_id)
    conflict → rollback, no-op, ack
  apply application operation
  persist state
  [insert outbox]
COMMIT
→ ack Kafka message
```

- Уникальность `(consumer, event_id)` обеспечивает storage engine ([ADR 11](adr/0011-transactional-inbox-and-idempotent-event-processing.md#хранилища-и-конкурентная-обработка)): unique constraint PostgreSQL, unique index MongoDB.
- В PostgreSQL вставка — `INSERT … ON CONFLICT DO NOTHING` с проверкой числа вставленных строк: нарушение unique constraint без `ON CONFLICT` переводит транзакцию в aborted state. Параллельная транзакция с тем же `event_id` ждёт первую и после её commit получает конфликт; после её rollback — продолжает как первая.
- В MongoDB duplicate key error откатывает транзакцию и классифицируется как duplicate.
- Insert-first не меняет гарантию ADR 11, допускающего любой порядок операций при условии, что business state и Outbox не фиксируются без записи Inbox; он даёт раннее обнаружение duplicate до выполнения бизнес-операции.
- UoW охватывает все локальные изменения обработки: Inbox, state и Outbox. Только тогда обработка effectively-once.
- Duplicate — не ошибка: транзакция откатывается (изменений нет), сообщение подтверждается, метрика duplicate увеличивается.

Kafka acknowledgement (commit offset) выполняется только после успешного commit.

#### Действия после commit в Inbox-сценариях

Inbox делает повтор no-op, поэтому внешнее действие после commit (например, Signal Temporal workflow в `orders` apply-сценариях) при сбое между commit и действием не будет повторено обычной повторной доставкой. Такой slice выбирает одно из решений и документирует его:

- действие идемпотентно по ключу, производному от `event_id`, и duplicate-ветка consumer'а повторяет его;
- действие выполняется до UoW и само идемпотентно;
- эффект переносится за Outbox.

Аналогично для dedicated consumer SpiceDB-relationships: идемпотентный `TOUCH`/`DELETE` выполняется до UoW, затем UoW фиксирует Inbox. Повтор повторяет идемпотентный write и получает duplicate ([документация security](security.md#dual-write-rules)).

## Temporal

- Workflow не владеет транзакцией и не знает о UoW, как и о Commands и handlers ([ADR 6](adr/0006-ddd-structure-inside-vertical-slices.md#входы-в-application)).
- Каждая Activity → Command → application handler → собственный UoW (state + Outbox).
- Activities не объединяются одной транзакцией. Атомарность процесса достигается локальными транзакциями, durable orchestration и compensation, а не общей транзакцией.
- Повтор Activity после commit не создаёт второй логический outbox event: операция идемпотентна по ключу, производному от Workflow ID и шага, или по ожидаемой `aggregate_version` ([ADR 13](adr/0013-transactional-outbox-for-integration-event-publishing.md#стабильность-event_id)). Повтор находит уже применённое изменение и возвращает прежний результат без записи state и Outbox.
- Use case, который запускает workflow или передаёт ему Signal/Update, делает это через порт Temporal после commit своего UoW, а не внутри него. Запуск идемпотентен по Workflow ID.

## gRPC и BFF

Request не определяет транзакцию. Границу транзакции определяет use case: один handler может не использовать UoW, использовать один UoW или выполнить внешний вызов между двумя UoW (`payments`).

Запрещены:

- middleware или interceptor «каждый gRPC request в одной DB transaction»;
- открытие транзакции в transport adapter'е вокруг вызова handler'а (исключение — Inbox-обёртка consumer'а, описанная выше, которая работает в той же модели propagation);
- транзакция, охватывающая несколько вызовов gRPC.

BFF не имеет доменного хранилища и UoW не использует.

## Authorization

Порядок для защищённой state-changing операции:

```text
authorize (CheckPermission)
  → [SpiceDB relationship write при создании resource, с заранее сгенерированным id]
  → UoW { domain operation, persist state, [ZedToken], [Outbox] }
```

- SpiceDB не входит в UoW.
- Creation/owner relationship пишется до `Do` с id, сгенерированным application. Ошибка write — fail closed, транзакция не начинается. Ошибка commit после успешного write оставляет orphan relationship для reconciliation ([ADR 18](adr/0018-spicedb-for-authorization.md#relationship-lifecycle-и-dual-write)).
- Полученный ZedToken сохраняется рядом с resource внутри UoW.
- Окно между check и commit допустимо. Если в нём возможна значимая race (например, права отозваны между check и изменением), она закрывается доменными инвариантами или отдельной consistency strategy use case, а не переносом SpiceDB в транзакцию.

## Storage-specific

### `orders` / PostgreSQL

Основная и первая реализация UoW:

- state + Outbox;
- Inbox + state;
- Inbox + state + Outbox;
- optimistic concurrency по `version`;
- idempotent commands: idempotency key или ожидаемая `aggregate_version` в той же транзакции.

CQRS queries (`getorder`, `listcustomerorders`) выполняются без UoW: одиночные read statements в autocommit.

### `payments`

Локально в UoW: payment state + operation record + Outbox. Provider call выполняется вне транзакции:

```text
UoW { record operation (pending, idempotency key) }
  → provider call (idempotency key)
  → UoW { operation result + payment state + Outbox }
```

Возможен и порядок `provider call → local transaction`, если его допускает idempotency policy провайдера и use case. Выбор фиксируется в документации use case. Иллюзии атомарности с provider нет: сбой между вызовом и локальным commit восстанавливается повтором с тем же idempotency key, callback'ом или operation result. Webhook-дедупликация — отдельная идемпотентная boundary в той же транзакции, что и изменение state (аналог Inbox).

### `restaurants` / MongoDB

- По умолчанию — single-document atomicity без транзакции.
- UoW с MongoDB session/transaction — только для multi-document изменений или `state + Inbox/Outbox`.
- Multi-document transactions не используются без необходимости; если документную модель можно построить так, чтобы изменение было в одном документе, это предпочтительнее.
- Транзакции MongoDB требуют replica set, в том числе локально и в тестах.

### `delivery`

Хранилище не выбрано. При выборе подтверждаются:

- transaction capabilities и isolation;
- атомарная запись state + Inbox/Outbox и уникальность `(consumer, event_id)`;
- защита конкурентных courier assignments (`Accept Delivery Offer`): ровно один актуальный offer становится `Courier Assignment` — через unique constraint, `FOR UPDATE` или версию;
- корректный rollback при отмене async-задачи посреди транзакции.

## Ошибки

| Исход `fn` / commit | Поведение |
| --- | --- |
| Успех | Commit |
| Application или domain error | Rollback, ошибка возвращается без изменений |
| Infrastructure error | Rollback; retry только для причин из [retry policy](#retry-policy) |
| Panic / exception | Rollback и propagate (в Go — `recover`, rollback, повторный `panic`) |
| Отмена `ctx` / deadline | Rollback, ошибка отмены |
| Ошибка commit | Не успех: ошибка возвращается вызывающему |
| Неизвестный результат commit (потеря соединения во время `COMMIT`) | Не повторяется UoW автоматически; возвращается отдельной ошибкой |

Неизвестный результат commit разрешается идемпотентностью команды, а не догадкой UoW: Kafka повторно доставит событие и Inbox покажет duplicate, клиент повторит запрос с тем же idempotency key, Temporal повторит Activity с ожидаемой `version`. Поэтому каждая операция с UoW, которую могут повторить, должна иметь один из этих механизмов.

Rollback после ошибки не маскирует исходную ошибку: ошибка rollback логируется и добавляется к исходной.

## Observability

UoW-реализация в infrastructure предоставляет:

| Сигнал | Тип |
| --- | --- |
| Длительность транзакции | Гистограмма |
| Commits / rollbacks | Counter с результатом |
| Commit failures, включая неизвестный результат | Counter с классом ошибки |
| Retries | Counter с причиной: `serialization`, `deadlock`, `version_conflict`, `transient` |
| Deadlock и serialization failures | Counter |

Labels — низкокардинальные: boundary, use case (если известен), результат, причина или класс ошибки. Идентификаторы aggregates, `event_id` и subject не используются как labels ([observability](observability.md#метрики)).

Транзакция получает span (или атрибуты существующего span use case) с числом попыток и результатом. Ручные spans на каждый SQL statement не создаются, если включена driver instrumentation ([observability](observability.md#postgresql-и-mongodb)). Retry и rollback логируются с причиной.

## Testing

### Unit

- Handlers тестируются с test UoW, который выполняет `fn` и фиксирует факт commit/rollback, для проверки orchestration: что сохраняется, что пишется в Outbox, что при ошибке ничего не считается зафиксированным.
- Domain tests работают без UoW.

### Integration (Testcontainers)

PostgreSQL:

- commit и rollback;
- state + Outbox: rollback не оставляет запись Outbox;
- Inbox + state + Outbox;
- гонка unique constraint Inbox: два параллельных обработчика одного `event_id`;
- optimistic concurrency conflict;
- serialization retry при `SERIALIZABLE`/`REPEATABLE READ`;
- participation внутреннего `Do` во внешнем scope.

MongoDB:

- replica set в Testcontainers (без него транзакций нет);
- single-document atomicity;
- multi-document transaction — только в сценариях, где она используется.

### Fault

- ошибка до commit;
- ошибка сохранения aggregate;
- ошибка Outbox insert;
- duplicate Inbox;
- commit failure через разрыв соединения через proxy (например, Toxiproxy), а не mock;
- конкурентная обработка одного aggregate.

Проверяется итоговое состояние хранилища, а не только вызовы. Fault scenarios Inbox/Outbox из [документации событийного взаимодействия](event-driven-architecture.md#тестирование) остаются в силе.

## Где появится UoW

UoW подключается вместе с реализацией соответствующего SDD change; пустые программные scaffolds не создаются.

Application-слой всех четырёх boundaries уже реализован с in-memory адаптерами UoW (changes `implement-{orders,restaurants,delivery,payments}-application-layer`); production-реализаций поверх хранилищ пока нет. Отличия реализации от таблиц ниже отмечены в соответствующих разделах.

### `orders`

| Use case | UoW | Состав транзакции |
| --- | --- | --- |
| `placeorder` | Да | `Order` + idempotency key + ZedToken + [Outbox]; SpiceDB owner relationship до, start workflow после commit |
| `requestordercancellation` | Если сохраняется запрос отмены вместе с Outbox | `Order` + [Outbox]; Signal workflow после commit. Реализован без UoW: запрос отмены не сохраняется локально и передаётся workflow после authorization и проверки `CheckCancellation` ([design.md change `implement-orders-application-layer`](../openspec/changes/archive/2026-10-25-implement-orders-application-layer/design.md)) |
| `confirmorder`, `rejectorder` | Да | Inbox + `Order` + [Outbox]; Signal после commit |
| `recordpreparationstarted`, `recordorderready` | Да | Inbox + `Order` + [Outbox]; Signal после commit |
| `recordcourierassignment`, `recordpickup`, `recorddeliverycompletion`, `recorddeliveryfailure` | Да | Inbox + `Order` + [Outbox]; Signal после commit |
| `recordcourierrelease` | Да | Inbox + назначение курьера в `orders` + DELETE relationships SpiceDB до commit; `Order` и workflow не меняются |
| `completeorder` | Да | `Order` + Outbox; идемпотентность по ожидаемой `version` или ключу шага |
| `failorcancelorder` | Да | `Order` + Outbox; идемпотентность по ожидаемой `version` или ключу шага |
| `fulfillment` (Temporal-адаптер) | Нет | Workflow; Activities вызывают handlers |
| `getorder`, `listcustomerorders` | Нет | CQRS queries |

### `restaurants`

| Slice | UoW | Состав транзакции |
| --- | --- | --- |
| `accept-order`, `reject-order`, `start-preparation`, `mark-ready-for-pickup` | Да | `Restaurant Order` + Outbox |
| `open-restaurant`, `close-restaurant`, `set-menu-item-availability` | Если публикуют бизнес-факт | Документ + Outbox |
| `receive-order-request` | Если публикует событие или хранит idempotency key отдельно | `Kitchen Order` + [idempotency] + [Outbox] |
| `register-restaurant`, `update-restaurant`, `create-menu`, `add-menu-item`, `update-menu-item`, `cancel-restaurant-order` | Нет по умолчанию | Single-document; UoW, только если document boundaries потребуют нескольких документов или Outbox |
| `get-restaurant`, `list-restaurants`, `get-menu` | Нет | Queries |

Реализация application-слоя `restaurants` ([design.md change `implement-restaurants-application-layer`](../openspec/changes/implement-restaurants-application-layer/design.md)) уточняет таблицу:

- `open-restaurant`, `close-restaurant`, `set-menu-item-availability` публикуют бизнес-факт (`RestaurantOpened`, `RestaurantClosed`, `MenuItemAvailabilityChanged`) и используют UoW;
- `update-menu-item` выполняется в UoW: при изменении доступности он записывает тот же `MenuItemAvailabilityChanged` в Outbox вместе с `Menu`;
- `report-preparation-delay` и `report-preparation-failure` — UoW: `Kitchen Order` + Outbox;
- `cancel-restaurant-order` — UoW: `Kitchen Order` + Outbox (`RestaurantOrderCancelled`);
- `receive-order-request`, `queue-restaurant-order` и `mark-assembled` — без UoW: один save, факты `RestaurantOrderReceived`, `RestaurantOrderQueued`, `RestaurantOrderAssembled` локальные; идемпотентность Receive — через уникальность `orderId` в repository. В MVP `accept-order` ставит заказ в Kitchen Queue в своей UoW, а `queue-restaurant-order` покрывает заказы, оставшиеся в `ACCEPTED`.

### `payments`

| Slice | UoW | Состав транзакции |
| --- | --- | --- |
| `authorize_payment`, `capture_payment`, `cancel_authorization`, `refund_payment` | Да, вокруг provider call | Operation record (pending) → provider → operation result + `Payment` + Outbox |
| `apply_provider_callback` | Да | Webhook dedup + operation/`Payment` + Outbox |
| `apply_provider_operation_result` | Да | Operation record + `Payment` + Outbox |
| `create_payment` | Если idempotency key хранится отдельно от `Payment` | `Payment` + idempotency |
| `get_payment_status`, `list_payment_operations` | Нет | Queries |

### `delivery`

| Slice | UoW | Состав транзакции |
| --- | --- | --- |
| `accept_delivery_offer` | Да | `Delivery Offer` + `Courier Assignment` + резервирование курьера и доставки + Outbox; контроль конкуренции. В текущей реализации дополнительно withdraw остальных pending offers этой `Delivery` |
| `reassign_courier` | Да | Завершение текущего назначения + `Delivery` + освобождение курьера + [Outbox] |
| `replan_delivery` | Да | Одна UoW под одной проверкой `ReplanDelivery`: [завершение назначения, как `reassign_courier`] + выбор кандидата + offer |
| `confirm_pickup` | Да | `Delivery` + Outbox |
| `confirm_delivery`, `report_delivery_failure` | Да | `Delivery` + освобождение `Courier` и DELETE `delivery#courier` до commit + Outbox. Освобождение курьера при failure — текущее допущение реализации ([design.md change `implement-delivery-application-layer`](../openspec/changes/implement-delivery-application-layer/design.md)) |
| `cancel_delivery` | Да | `Delivery` (`waiting\|assigned → cancelled`) + освобождение `Courier`, если он был назначен + Outbox |
| `create_delivery`, `offer_delivery` | Если публикуют событие или хранят idempotency key отдельно | Запись + [idempotency] + [Outbox] |
| `register_courier`, `set_courier_availability`, `reject_delivery_offer`, `find_courier` | Нет по умолчанию | Одиночное изменение |
| `update_courier_location` | Нет | Высокочастотная одиночная запись; транзакция не нужна |
| `get_courier_current_delivery`, `list_available_delivery_offers` | Нет | Queries |

#### Исключение: offer, `Delivery` и `Courier` в одной транзакции

Accept Delivery Offer, Reassign Courier и сценарии завершения и отмены (Confirm Delivery, Report Delivery Failure, Cancel Delivery) изменяют несколько aggregates одной локальной транзакцией: offer (только при принятии), `Delivery` и `Courier` вместе с Outbox.

- **Причина:** инвариант «у курьера не больше одной активной доставки» и атомарность Assignment (FR-310) нельзя обеспечить eventual consistency: между занятием курьера и назначением доставки в отдельных транзакциях курьер получил бы второе назначение или остался бы занятым без доставки.
- **Границы слайсов:** use cases слайса `delivery` не вызывают методы агрегата `Courier` и `CourierRepository`. Занятость меняется только через прикладной порт `CourierAssignment` слайса `courier` (`ensure_available`, `occupy`, `release`); его реализация загружает и сохраняет `Courier` через `CourierRepository`, то есть в активной UoW вызывающего use case (FR-485). Правила доступности остаются в агрегате `Courier`.
- **Конкуренция:** запись `Courier` защищена optimistic concurrency по `version`; конкурентное назначение того же курьера получает конфликт и откатывает всю транзакцию.
- **Проверка:** сбой commit не оставляет курьера занятым, offer принятым и `Delivery` назначенной (AC-328, AC-550).

## Открытые вопросы реализации

- драйвер PostgreSQL в `orders` и способ передать executor транзакции в `watermill-sql` publisher;
- формат хранения idempotency key команд и сохранённого результата (общий с [event-driven architecture](event-driven-architecture.md#открытые-вопросы-реализации));
- точные параметры retry (число попыток, backoff) после нагрузочных данных;
- имена метрик и buckets гистограмм;
- механизм propagation в `payments` и `delivery` после выбора хранилищ;
- стратегия повторения Signal Temporal для duplicate-ветки apply-сценариев `orders`. Выбран первый вариант из [«Действия после commit в Inbox-сценариях»](#действия-после-commit-в-inbox-сценариях): duplicate-ветка повторно уведомляет workflow с тем же `event_id`, workflow дедуплицирует его ([design.md change `implement-orders-application-layer`](../openspec/changes/archive/2026-10-25-implement-orders-application-layer/design.md)); вопрос остаётся до реализации Temporal adapter'а.

# 21. Pure FSM Transitions

Date: 2026-10-24

## Status

Accepted

Связан с [6. DDD Structure Inside Vertical Slices](0006-ddd-structure-inside-vertical-slices.md), [11. Transactional Inbox and Idempotent Event Processing](0011-transactional-inbox-and-idempotent-event-processing.md), [13. Transactional Outbox for Integration Event Publishing](0013-transactional-outbox-for-integration-event-publishing.md) и [20. Unit of Work for Local Transactions](0020-unit-of-work-for-local-transactions.md).

Для `orders` связан с [локальным ADR 2: Использовать Temporal для оркестрации заказов](../../boundaries/orders/docs/adr/0002-use-temporal-to-orchestrate-orders.md).

## Context

Агрегаты `Order`, `Delivery` и `Payment` имеют business lifecycle с допустимыми и недопустимыми изменениями состояния. FSM удобна для явного описания этих правил, но многие библиотеки одновременно предлагают callbacks, hooks, event bus и runtime state. Если использовать их для persistence, сообщений или orchestration, доменная модель начинает зависеть от infrastructure, переход перестаёт быть детерминированным, а тест требует внешнего окружения.

[ADR 6](0006-ddd-structure-inside-vertical-slices.md) уже требует держать бизнес-инварианты в domain model и не допускать её зависимости от Kafka, ORM, Temporal SDK и transport types. [ADR 20](0020-unit-of-work-for-local-transactions.md) помещает transaction и persistence в application/infrastructure, а Inbox и Outbox ADR определяют надёжную обработку integration events вне домена. Локальный Temporal ADR оставляет инварианты `Order` независимыми от Workflow.

Требуется уточнить границу: что именно делает FSM, как выражаются переходы, ошибки и domain events, и где выполняются эффекты после результата перехода. Подробные правила и примеры приведены в [документации FSM](../fsm.md).

На этом шаге фиксируются только документация и архитектурное решение. Исходный код, library dependencies, persistence schemas и adapters не добавляются.

## Decision

FSM является чистой детерминированной частью domain model:

```text
state + trigger + domain inputs = next state | typed domain error
```

Она хранит текущее business state агрегата, определяет допустимые triggers и guards, вычисляет следующее состояние и отклоняет недопустимые переходы. Guards используют только domain data или явно переданные чистые значения.

### Transition table как данные

Переходы выражаются единой transition table как данные. Эта таблица служит одним источником для transition engine, table-driven тестов и генерации диаграмм или документации. Правила переходов не дублируются условными ветками в handlers и adapters.

Библиотека допустима только как чистый transition engine. `OnEnter`, `OnLeave`, `BeforeTransition`, `AfterTransition`, hooks, callbacks и встроенный event bus не используются для orchestration или внешних действий. Если библиотека провоцирует effectful переходы или скрывает таблицу, используется простая собственная реализация.

### Иерархия состояний

Transition table может иметь иерархию состояний — подмножество statecharts. Это способ записать ту же таблицу без повторов, а не другой механизм; формула перехода не меняется. Плоские таблицы иначе повторяют одно правило для группы состояний (`Cancel` пять раз в `RestaurantOrder`), а группы дублируются руками (`TERMINAL_STATES`, `WITH_ACTIVE_ASSIGNMENT`, `IsFinal()`, списки `$in` в адаптерах).

- Состояния делятся на leaf и compound; родитель каждого узла задан данными, узлы верхнего уровня помечены явно. Aggregate всегда ровно в одном leaf state; persistence, Protobuf, integration events и метрики используют только leaf. Compound states — термины Ubiquitous Language контекста и наружу не попадают.
- Переход объявляется на leaf или compound state. Target — leaf state либо targetless `stay` (aggregate остаётся в текущем leaf и меняет только данные).
- Разрешение `(leaf, trigger)` — innermost-first: от leaf вверх по предкам, побеждает ближайшая строка; строка вложенного состояния переопределяет строку предка. Пара `(node, trigger)` уникальна. Повторы `AlreadyInState` объявляются только на leaf. Строк на корне нет.
- Принадлежность к группе выражается функциями модели (`isIn`, `leavesOf`), а не ручными списками в domain, application и adapters.
- Не используются: entry/exit actions, delayed (`after`) и eventless (`always`) transitions, history states, параллельные регионы. Независимое измерение агрегата — отдельное поле или entity со своей таблицей.

Рассмотренные statechart-библиотеки (2026-10-26) не подходят как engine: XState 5 (`transition()` чистая, но guard=false и необработанное событие неразличимы и без причины — проверено прогоном; compound state обязан иметь `initial`), qmuntal/stateless (guard — `bool`, unhandled trigger — panic, мутирующий `Fire`), statig (переходы в макросах, нет интроспекции, неявный `Top`), python-statemachine 3.x (экземпляр с callbacks, guard — `bool`), Sismic (интерпретатор, LGPL-3.0). Ни одна не даёт `GuardFailed{reasons}` и `AlreadyInState` без обёртки, повторяющей разрешение перехода, поэтому engine — собственный в каждом языке.

### Граница aggregate и Temporal

Каждый aggregate имеет собственную FSM. `Order`, `Delivery` и `Payment` не объединяются общей машиной; взаимодействие между ними выполняется через commands и events.

FSM описывает только business state aggregate. Temporal Workflow владеет orchestration stage, ожиданиями, retries, timers и compensation state. Workflow state не добавляется в FSM `Order`, а aggregate state не хранится в Temporal как source of truth. Aggregate `version` для optimistic concurrency находится вне FSM.

### Результат и ошибки

Успешный переход возвращает явный domain result, после чего aggregate обновляет state. Ошибки переходов типизированы:

- `InvalidTransition{from, trigger}`;
- `GuardFailed{reason}`;
- `AlreadyInState{state}`.

FSM строгая: повторный trigger в целевом состоянии возвращает `AlreadyInState`, а не success. Такой исход явно присутствует в таблице. Application handler по бизнес-семантике конкретного use case решает, превратить ли повтор в идемпотентный no-op или вернуть ошибку. Infrastructure сопоставляет ошибку с transport contract, например `FAILED_PRECONDITION`; FSM не знает transport codes.

### Domain methods и events

Domain method выполняет method-specific validation, вызывает FSM, обновляет state и возвращает domain result. Infrastructure actions внутри метода и перехода запрещены.

Значимые domain events записываются как данные в памяти агрегата. Aggregate предоставляет явный механизм забора событий наподобие `PullEvents()`. Application решает, какие domain events публикуются, infrastructure маппит их в integration events и сохраняет Outbox вместе с aggregate state ([ADR 25](0025-integration-event-publication.md)). FSM не создаёт event автоматически на каждый переход.

### Эффекты и integrations

FSM не знает о Kafka, Inbox, Outbox, repositories, Unit of Work, SQL/MongoDB, gRPC/HTTP, Temporal, SpiceDB, OpenTelemetry, logging, metrics, filesystem, network, DI или внешних callbacks.

Application координирует поток `authorize → UoW { load → domain method → FSM → persist state → persist Outbox } → commit`. Kafka consumer и Temporal Activity преобразуют transport input в Command и чистые domain values до вызова handler/domain model. Решение о Signal Temporal после изменения state также находится выше FSM.

### Time, random и identifiers

FSM не читает system clock и random source и не генерирует identifiers. Application получает время через clock abstraction и передаёт его значением. Идентификаторы создаются application layer либо domain factory с явно переданным значением.

### Persistence

Сохраняется явный domain state как enum/string, а не внутренние структуры FSM-библиотеки. Repository adapter восстанавливает aggregate из persisted state; FSM использует восстановленное состояние aggregate. Persistence mapping и optimistic concurrency не входят в transition engine.

### Testing и observability

FSM тестируется полностью in-memory таблицей `from, trigger, inputs → to | typed error`, без Docker, Testcontainers, databases, Kafka, Temporal и mocks внешних систем. Guards имеют отдельные boundary tests. Тест полноты обязан доказать, что каждая пара leaf `state × trigger` имеет определённый исход, включая `InvalidTransition` и `AlreadyInState`; ожидаемая матрица пишется независимо от таблицы. Для иерархической таблицы структурные тесты проверяют отсутствие циклов, наличие потомков у compound state, уникальность `(node, trigger)` и объявление повторов только на leaf.

BDD проверяет бизнес-смысл поведения, а не механизм библиотеки. Application/infrastructure может наблюдать переход с полями `aggregate_type`, `from_state`, `trigger`, `to_state` и результатом; FSM не зависит от observability SDK.

## Consequences

Правила lifecycle становятся воспроизводимыми и проверяемыми без infrastructure. Единая таблица предотвращает расхождение между кодом, тестами и диаграммами, а типизированные ошибки сохраняют domain semantics до transport boundary.

Строгий `AlreadyInState` не смешивает корректность domain transition с идемпотентностью delivery или use case. Каждый application handler обязан явно определить политику повтора. Domain events также требуют осознанного выбора: переход сам по себе не публикует integration event.

Application и infrastructure получают больше явной координации: они забирают domain events, сохраняют state и Outbox, выполняют authorization, маппят ошибки, вызывают Temporal и записывают telemetry. Это намеренная цена независимой domain model.

FSM-библиотека не является архитектурной зависимостью. При выборе библиотеки потребуется проверить представление таблицы, детерминированность, восстановление из явного persisted state и возможность полностью отказаться от effectful callbacks; иначе будет реализован небольшой табличный engine.

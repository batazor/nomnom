# Finite State Machines

Finite State Machine (FSM) — чистая часть domain model агрегата. Она описывает только допустимое изменение business state:

```text
state + trigger + domain inputs = next state | typed domain error
```

При одинаковых значениях `state`, `trigger` и domain inputs результат всегда одинаков. FSM не выполняет побочных эффектов и не зависит от способа вызова, хранения или наблюдения агрегата.

Архитектурное решение зафиксировано в [ADR 21: Pure FSM Transitions](adr/0021-pure-fsm-transitions.md). Оно согласуется с разделением domain/application/infrastructure из [ADR 6](adr/0006-ddd-structure-inside-vertical-slices.md), локальной транзакционной границей из [ADR 20](adr/0020-unit-of-work-for-local-transactions.md) и оркестрацией заказов из [локального Temporal ADR](../boundaries/orders/docs/adr/0002-use-temporal-to-orchestrate-orders.md).

## Ответственность FSM

FSM владеет:

- текущим business state агрегата;
- набором допустимых triggers;
- guards, использующими только domain model и явно переданные чистые значения;
- вычислением следующего состояния;
- отклонением недопустимых переходов типизированной domain error.

Transition table выражается как данные и является единым источником для transition engine, table-driven тестов и генерации диаграмм или документации. Бизнес-правила переходов не дублируются россыпью `if`/`switch` в handlers, adapters и callbacks. Языковое представление таблицы может различаться, но каждая запись явно задаёт исходное состояние, trigger, guard или безусловное правило и результат.

На один aggregate приходится одна FSM: `Order`, `Delivery` и `Payment` имеют независимые модели состояний. Переход между aggregates выполняется через commands и events, а не через общую FSM. Совпадение названий состояний в разных bounded contexts не создаёт общий тип или общую transition table.

FSM моделирует business state агрегата. Orchestration stage, ожидания, retries, timers и compensation принадлежат Temporal Workflow и не становятся состояниями `Order`. Например, `cancelled` может быть состоянием `Order`, а `waiting_restaurant` и `compensating` — только workflow stages. `version`, используемая для optimistic concurrency агрегата, также находится вне FSM.

## Что находится вне FSM

FSM не знает о существовании:

- Kafka, Outbox и Inbox;
- repositories, Unit of Work, SQL, MongoDB и persistence models;
- gRPC, HTTP, Protobuf и transport codes;
- Temporal Activities, Workflow, start, Signal и Update;
- SpiceDB и authorization;
- OpenTelemetry, logging и metrics;
- filesystem, network, system clock и random;
- DI containers и composition roots;
- callbacks, hooks и event bus библиотеки.

Guards не обращаются к repository, clock или внешнему service. Данные, необходимые для решения, уже находятся в aggregate либо передаются как чистые domain values.

## Specification и вне-state guards

Transition table остаётся единственным источником допустимости trigger по текущему state. Specification не заменяет строку таблицы и не дублирует проверку state.

Переиспользуемое условие вне state — временное окно, сумма, зона, capacity, расписание или лимит — может быть выражено чистой domain Specification. Aggregate method вычисляет её перед trigger либо guard transition table делегирует ей проверку; одно правило вызывается в одном месте.

```text
order.Cancel(now, policy)
  → WithinCancellationWindow(order, now, policy)
  → FSM.Trigger(Cancel)
  → update state
  → domain result
```

Unsatisfied Specification возвращает типизированные reasons, которые aggregate оборачивает в domain error. `InvalidTransition`, `AlreadyInState` и state-based часть `GuardFailed` по-прежнему принадлежат FSM. Specification получает `now` и другие внешние данные значениями и не обращается к clock, repository или infrastructure. Полные правила зафиксированы в [Specification Pattern](specification-pattern.md).

## Transition table и результат

Концептуальная запись таблицы выглядит так:

```text
from_state | trigger | guard | to_state | failure
```

Transition engine сначала находит определённый исход для пары `(state, trigger)`, затем вычисляет guard над domain inputs и возвращает значение результата. Он не выполняет action. Успешный результат содержит как минимум `from`, `trigger` и `to`; aggregate применяет `to` к своему состоянию. При необходимости domain method добавляет к результату бизнес-данные, но не transport или infrastructure instructions.

Ошибки перехода типизированы:

- `InvalidTransition{from, trigger}` — trigger не разрешён из текущего состояния;
- `GuardFailed{reason}` — переход известен, но его чистое domain-условие не выполнено;
- `AlreadyInState{state}` — trigger семантически повторяет уже применённый переход в его целевом состоянии.

FSM строгая: повторный trigger в целевом состоянии не считается успешным переходом. Для таких повторов таблица содержит явный исход `AlreadyInState`, поэтому поведение остаётся полным и проверяемым, а не зависит от скрытого special case engine. Application handler интерпретирует эту ошибку по бизнес-семантике конкретного use case: может превратить идемпотентный повтор или duplicate Signal в no-op либо вернуть ошибку. Infrastructure сопоставляет domain/application error с transport contract, обычно `FAILED_PRECONDITION`; FSM не знает gRPC/HTTP codes и `ErrorInfo`.

## Statecharts: иерархия состояний

Transition table может иметь иерархию состояний — подмножество statecharts, принятое в [ADR 21](adr/0021-pure-fsm-transitions.md#иерархия-состояний). Это не другой механизм, а способ записать ту же таблицу без повторов.

- **Leaf state** — атомарное состояние, в котором aggregate находится, которое сохраняется, публикуется и попадает в метрики. Aggregate всегда ровно в одном leaf.
- **Compound state** — именованная группа leaf states или других compound states. Это термин Ubiquitous Language (`IN_KITCHEN`, «заказ у кухни»), а не техническая группа; наружу и в хранилище не попадает.
- Родитель каждого узла задан данными; узлы верхнего уровня помечены явно.

Строка таблицы имеет форму:

```text
source (leaf | compound) | trigger | guard | target (leaf | stay) | failure
```

`stay` — targetless transition: aggregate остаётся в текущем leaf и меняет только свои данные (`ReportDelay`, `RecordFact`). Это не `AlreadyInState`: trigger не повторяет уже применённый переход.

Разрешение пары `(leaf, trigger)` — innermost-first:

```text
for node in [leaf, parent(leaf), …, top-level]:
  row = table[(node, trigger)]          → guard → target | GuardFailed
  repeat = repeats[(node, trigger)]     → AlreadyInState   (только для leaf)
none → InvalidTransition
```

Строка вложенного состояния переопределяет строку предка для того же trigger — например, `Cancel` на `ACTIVE_PREPARATION` с guard политики отмены переопределяет безусловный `Cancel` на `OPEN`. Строк на корне нет: переход «из любого состояния» был бы неявным поведением.

Принадлежность к группе — функции модели, а не ручные списки: `isIn(state, node)` для loaded aggregate и `leavesOf(node)` для фильтров репозитория. `TERMINAL_STATES`, `WITH_ACTIVE_ASSIGNMENT`, `IsFinal()` и списки `$in` в адаптерах выражаются через compound state.

Не используются: entry/exit actions (это callbacks), delayed (`after`) и eventless (`always`) transitions (время — у application и Temporal), history states (восстановление идёт из persisted leaf), параллельные регионы (независимое измерение — отдельное поле или entity со своей таблицей).

## Domain methods и domain events

Публичное поведение агрегата выражается domain methods. Например:

```text
order.Cancel(requestedAt, cancellationPolicy):
  validate method-specific conditions
  policyResult = WithinCancellationWindow(order, requestedAt, cancellationPolicy)
  if unsatisfied: return ErrOrderCannotBeCancelled{reasons}
  result = orderFSM.transition(order.state, Cancel, requestedAt)
  order.state = result.to
  record OrderCancelled as in-memory domain data when this business fact matters
  return domain result
```

Порядок — validate conditions, вычислить применимые вне-state Specifications, вызвать FSM, обновить state и вернуть domain result. Метод не сохраняет aggregate и не публикует сообщения.

Domain events — данные о значимых бизнес-фактах, записанные в памяти агрегата, например `OrderCancelled`. Aggregate предоставляет механизм забора накопленных событий наподобие `PullEvents()`. Application получает события после domain operation, а infrastructure преобразует нужные из них в integration events и сохраняет Outbox в одной локальной транзакции со state.

FSM не создаёт event автоматически на каждый переход. Наличие transition не означает, что появился отдельный бизнес-факт для интеграции. Выбор domain event остаётся явным решением domain method.

Transition callbacks (`OnEnter`, `OnLeave`, `BeforeTransition`, `AfterTransition` и аналоги) не используются для external actions, создания Outbox records, публикации сообщений, persistence, telemetry или запуска Temporal. Предпочтителен явный результат перехода, который обрабатывают aggregate и application layer.

## Application и infrastructure flow

Для защищённого синхронного сценария основной поток выглядит так:

```text
authorize
  → UnitOfWork {
      load aggregate
      → domain method
      → FSM
      → persist aggregate
      → persist Outbox
    }
  → commit
```

В остальных entrypoints сохраняется та же domain operation:

```text
Kafka → Inbox → handler → domain method → FSM
FSM/domain result → application → Outbox → Kafka
Temporal Workflow → Activity → handler → domain method → FSM
Kafka → application → FSM → resulting state → Signal, if the use case requires it
```

Kafka consumer преобразует event payload в Command и чистые domain inputs до вызова domain model. Inbox дедуплицирует transport delivery, но не меняет строгость FSM. Activity также преобразует свой payload в Command и вызывает обычный handler. Workflow не вызывает FSM напрямую и не хранит aggregate state.

Authorization выполняется до domain operation. FSM и guards не вызывают SpiceDB и не принимают authorization decision за application layer. UoW обеспечивает атомарность persistence state, Inbox и Outbox, но transaction, repository и commit не входят в модель перехода.

## Time и identifiers

FSM не читает system clock и не генерирует identifiers или random values. Application получает время через clock abstraction и передаёт его значением, например `order.Cancel(requestedAt)`. Идентификатор создаётся application layer либо domain factory, которой значение передано явно. Зафиксированные входные значения делают переход воспроизводимым и детерминированным.

## Persistence и восстановление

Persistence сохраняет явный domain state как enum/string вместе с остальными данными агрегата. Внутренние структуры, runtime objects и конфигурация FSM-библиотеки не сериализуются.

Восстановление выполняется в направлении:

```text
persisted state → aggregate → FSM restored from the aggregate state
```

Repository adapter отвечает за mapping persistence model ↔ domain model. FSM не загружает и не сохраняет себя. Optimistic concurrency проверяет `version` отдельно от transition table.

## Testing

FSM проверяется полностью in-memory, без Docker, Testcontainers, databases, Kafka, Temporal и mocks внешних зависимостей.

Основной table-driven набор использует форму:

```text
from, trigger, inputs → to | typed error
```

Обязательны:

- позитивные переходы для каждой записи;
- недопустимые переходы с точным `InvalidTransition`;
- строгие повторы с `AlreadyInState`;
- отдельные boundary cases каждого guard с `GuardFailed{reason}`;
- тест полноты: каждая пара из декартова произведения всех leaf `state × trigger` имеет явно определённый исход;
- для иерархической таблицы — структурные проверки: иерархия без циклов, у compound state есть потомки, пара `(node, trigger)` уникальна, повторы объявлены только на leaf и не совпадают со строкой перехода того же leaf.

Тест полноты не требует, чтобы каждая пара была успешной: он требует отсутствия неявного поведения. Ожидаемая матрица пишется независимо от таблицы, поэтому перевод плоской таблицы в иерархию доказывается неизменной матрицей. BDD-сценарии проверяют бизнес-смысл, например возможность отмены заказа при заданных условиях, а не внутренний API или callback конкретной FSM-библиотеки.

## Observability

Application/infrastructure наблюдает вызов вокруг чистого перехода и при необходимости записывает:

```text
aggregate_type, from_state, trigger, to_state, result
```

Для ошибки `to_state` отсутствует, а `result` содержит низкокардинальный класс результата. Конкретные identifiers, payloads и причины guards следуют общей privacy/cardinality policy. FSM не импортирует observability SDK, не пишет logs и не изменяет metrics.

## Требования к библиотеке

Допустима библиотека, которая работает как чистый transition engine и позволяет выразить transition table как данные. Её внутренние callbacks, hooks и event bus не используются для orchestration или других side effects. Domain code не должен сохранять внутреннее состояние библиотечного runtime.

Если библиотека скрывает таблицу, требует effectful callbacks или подталкивает к external actions внутри перехода, используется простая собственная реализация над явной таблицей.

## Order

Statechart aggregate `Order` в `orders`; реализация — `boundaries/orders/slices/order/domains/order/fsm.go`. Fulfillment Facts — данные `Order`, а не states; их запись — targetless `record_fact`.

Compound states:

| Compound state | Входит | Смысл |
| --- | --- | --- |
| `open` | `placed`, `confirmed` | Open Order: Fulfillment не завершён; Order можно отменить и записать Fulfillment Fact |
| `closed` | `cancelled`, `completed` | Closed Order: terminal states (`IsFinal()`) |

```mermaid
stateDiagram-v2
    [*] --> placed: Place
    state open {
        placed --> confirmed: confirm
    }
    state closed {
        cancelled
        completed
    }
    confirmed --> completed: complete
    open --> cancelled: cancel
    open --> open: record_fact (stay)
    closed --> [*]
```

| source | trigger | guard | target | failure |
| --- | --- | --- | --- | --- |
| `placed` | `confirm` | — | `confirmed` | — |
| `confirmed` | `complete` | `occurred_at` задан | `completed` | `GuardFailed{occurred_at_required}` |
| `open` | `cancel` | `requested_at` задан | `cancelled` | `GuardFailed{requested_at_required}` |
| `open` | `record_fact` | `occurred_at` задан | `stay` | `GuardFailed{occurred_at_required}` |

Строгие повторы с явным `AlreadyInState`:

| from_state | trigger | Исход |
| --- | --- | --- |
| `confirmed` | `confirm` | `AlreadyInState{confirmed}` |
| `cancelled` | `cancel` | `AlreadyInState{cancelled}` |
| `completed` | `complete` | `AlreadyInState{completed}` |

Все остальные пары `state × trigger` дают `InvalidTransition`: `placed × complete` (Completion только после подтверждения), любой trigger из `closed`, кроме повторов. `record_fact` для Closed Order — `InvalidTransition`, который Fulfillment reflector трактует как поздний факт (`IsFinal()`). Факт `payment_authorized` (Record Payment Authorization) допускается только в `placed`: ресторан запрашивается после него, поэтому `confirmed` Order оплату уже прошёл; в другом state — `InvalidTransition`. Отдельного state оплаты нет: клиентский статус `PAID` BFF вычисляет из факта, окно отмены не меняется. Failure пока завершается тем же `cancelled` с видом итога `failure`.

`AcceptsCancellation()` и `IsFinal()` — принадлежность к `open` и `closed`, а не ручные списки states.

## Kitchen / Preparation

Statechart aggregate `RestaurantOrder` в `restaurants`; реализация — `boundaries/restaurants/slices/restaurantorder/domains/restaurantorder/fsm.ts`. Сценарии, use cases и бизнес-смысл states описаны в [Kitchen](kitchen.md); здесь — только модель переходов.

`RestaurantOrder` — один aggregate с одной FSM. `Kitchen Order` — роль того же aggregate в кухонном процессе, а не второй aggregate и не вторая FSM. Её states не совпадают с `Order State` и Workflow Stage `orders` и не импортируются ими.

Compound states:

| Compound state | Входит | Смысл |
| --- | --- | --- |
| `OPEN` | `RECEIVED`, `ACCEPTED`, `IN_KITCHEN` | Работа кухни над заказом не завершена; заказ можно отменить |
| `IN_KITCHEN` | `QUEUED`, `ACTIVE_PREPARATION` | Заказ принят в Kitchen Queue и имеет `expected_ready_at`; кухня может сообщить о задержке |
| `ACTIVE_PREPARATION` | `PREPARING`, `ASSEMBLED` | Ресурсы кухни уже тратятся (List Active Preparations); возможна неудача приготовления, отмена — по политике |
| `CLOSED` | `READY`, `PICKED_UP`, `REJECTED`, `CANCELLED`, `FAILED` | Работа кухни закончена; из них только `READY` ещё переходит в `PICKED_UP` |

```mermaid
stateDiagram-v2
    [*] --> RECEIVED: Receive
    state OPEN {
        RECEIVED --> ACCEPTED: Accept
        ACCEPTED --> QUEUED: Queue
        state IN_KITCHEN {
            QUEUED --> PREPARING: StartPreparation
            state ACTIVE_PREPARATION {
                PREPARING --> ASSEMBLED: MarkAssembled
            }
        }
    }
    state CLOSED {
        READY
        PICKED_UP
        REJECTED
        CANCELLED
        FAILED
    }
    RECEIVED --> REJECTED: Reject
    ASSEMBLED --> READY: MarkReady
    READY --> PICKED_UP: RecordPickup
    OPEN --> CANCELLED: Cancel
    ACTIVE_PREPARATION --> CANCELLED: Cancel [policy]
    ACTIVE_PREPARATION --> FAILED: ReportFailure
    IN_KITCHEN --> IN_KITCHEN: ReportDelay (stay)
    CLOSED --> [*]
```

`IN_KITCHEN × ReportDelay` — targetless (`stay`): заказ остаётся в своём leaf state. `READY` — успешное завершение работы кухни: заказ ждёт курьера на kitchen board. Pickup принадлежит `delivery`; `restaurants` узнаёт о нём из Integration Event `DeliveryPickedUp` (Record Pickup) и переводит заказ в `PICKED_UP` — он уходит с kitchen board. Pickup раньше `READY` (Delivery создаётся сразу после Acceptance) — `InvalidTransition`: consumer подтверждает событие как бизнес-исход, заказ остаётся в своём state.

| source | trigger | guard | target | failure |
| --- | --- | --- | --- | --- |
| `RECEIVED` | `Accept` | Ресторан принимает заказы, все позиции доступны (`RestaurantAcceptingOrders`, `MenuItemAvailable`) | `ACCEPTED` | `GuardFailed{reasons}` |
| `RECEIVED` | `Reject` | — | `REJECTED` | — |
| `ACCEPTED` | `Queue` | — | `QUEUED` | — |
| `QUEUED` | `StartPreparation` | — | `PREPARING` | — |
| `PREPARING` | `ChangeItem` | Переход позиции допустим по таблице Preparation Item | `stay` | Ошибка таблицы позиции |
| `PREPARING` | `MarkAssembled` | Все required items в `READY` (только item-level model) | `ASSEMBLED` | `GuardFailed{RequiredItemsNotPrepared}` |
| `ASSEMBLED` | `MarkReady` | — | `READY` | — |
| `READY` | `RecordPickup` | — | `PICKED_UP` | — |
| `OPEN` | `Cancel` | — | `CANCELLED` | — |
| `IN_KITCHEN` | `ReportDelay` | Новое `expected_ready_at` позже текущего и позже `now` | `stay` | `GuardFailed{NotLater}` |
| `ACTIVE_PREPARATION` | `ReportFailure` | — | `FAILED` | — |
| `ACTIVE_PREPARATION` | `Cancel` | `RestaurantOrderCancellable`, если policy введена; переопределяет `OPEN × Cancel` | `CANCELLED` | `GuardFailed{reasons}` |

Строгие повторы с явным `AlreadyInState`:

| from_state | trigger | Исход |
| --- | --- | --- |
| `ACCEPTED` | `Accept` | `AlreadyInState{ACCEPTED}` |
| `QUEUED` | `Queue` | `AlreadyInState{QUEUED}` |
| `PREPARING` | `StartPreparation` | `AlreadyInState{PREPARING}` |
| `ASSEMBLED` | `MarkAssembled` | `AlreadyInState{ASSEMBLED}` |
| `READY` | `MarkReady` | `AlreadyInState{READY}` |
| `PICKED_UP` | `RecordPickup` | `AlreadyInState{PICKED_UP}` — Record Pickup отвечает no-op |
| `REJECTED` | `Reject` | `AlreadyInState{REJECTED}` |
| `CANCELLED` | `Cancel` | `AlreadyInState{CANCELLED}` |
| `FAILED` | `ReportFailure` | `AlreadyInState{FAILED}` |

Все остальные пары `state × trigger` дают `InvalidTransition`. В том числе:

- `PREPARING × MarkReady` — заказ не становится Ready, минуя Assembly;
- `ACCEPTED/QUEUED × Reject` — Reject After Acceptance не разрешён до продуктового решения; при решении строки добавляются в таблицу, а не обходятся в handler;
- `QUEUED × Accept` и последующие States — `InvalidTransition`; так как в MVP Queue выполняется в одной UoW с Accept, повтор Accept Order почти всегда встретит `QUEUED`, и UI показывает актуальное состояние заказа;
- `READY × Cancel` и `PICKED_UP × Cancel` — application сопоставляет ошибку с бизнес-результатом `CancellationRejected{ALREADY_READY}`, который Activity `orders` не повторяет как technical failure.

Интерпретация `AlreadyInState` — application layer ([Transition table и результат](#transition-table-и-результат)). Предложение для kitchen board: Start Preparation и Mark Ready for Pickup превращают повтор в успешный no-op без нового Domain Event; Cancel Restaurant Order возвращает сохранённый результат отмены. Политика остаётся предложением до подтверждения продуктом.

Текущие допущения реализации ([design.md change `implement-restaurants-application-layer`](../openspec/changes/archive/2026-10-25-implement-restaurants-application-layer/design.md#допущения-по-открытым-продуктовым-вопросам)); продуктовые вопросы остаются открытыми:

- интерпретация повтора Start Preparation и Mark Ready for Pickup — обязательный параметр handler `onRepeat: "succeed-without-event" | "reject"` без значения по умолчанию, выбор в composition root;
- guard `Cancel` из `PREPARING`/`ASSEMBLED` — политика из обязательного параметра handler `cancellationDuringPreparation: "allow" | "reject"` без значения по умолчанию; `reject` даёт `GuardFailed{cancellation_during_preparation_not_allowed}`;
- Reject After Acceptance: строки `ACCEPTED/QUEUED × Reject` не добавлены, исход — `InvalidTransition`;
- Cancel до Receive не доходит до FSM: заказа нет, application возвращает `NotFoundError`, tombstone не вводится.

`ReportDelay` и `ChangeItem` — самостоятельные targetless triggers (`stay`). Это не `AlreadyInState`: trigger не повторяет уже применённый переход, а меняет данные aggregate (`expected_ready_at`, State позиции) в разрешённом State заказа. Source строки явно ограничивает, в каких States это допустимо.

### Preparation Item

Если item-level model входит в scope, lifecycle позиции — таблица entity внутри aggregate `RestaurantOrder`, а не FSM отдельного aggregate. Aggregate сначала проверяет order-level строку `PREPARING × ChangeItem`, затем применяет переход позиции; оба результата возвращаются одним domain result.

| from_state | trigger | guard | to_state |
| --- | --- | --- | --- |
| `PENDING` | `Start` | — | `PREPARING` |
| `PENDING` | `Complete` | Позиция не требует приготовления (кандидат: напитки) | `READY` |
| `PREPARING` | `Complete` | — | `READY` |
| `PENDING`, `PREPARING` | `Fail` | — | `FAILED` |
| `FAILED` | `Restart` | — | `PREPARING` |
| `PENDING`, `PREPARING`, `FAILED` | `Exclude` | Согласованное решение по недоступной позиции | `CANCELLED` |

Набор негативных states не окончательный. `FAILED` позиции не переводит заказ в `FAILED` автоматически: решение принимает кухня или согласованная policy. `CANCELLED` позиция перестаёт быть required. Повторы (`READY × Complete`, `PREPARING × Start` и подобные) дают `AlreadyInState`, остальные пары — `InvalidTransition`; тест полноты охватывает обе таблицы.

### Вне-state условия

Capacity кухни, окно отмены во время Preparation и доля потраченных ресурсов — не строки таблицы, а Specifications ([Specification Pattern](specification-pattern.md)). `RestaurantAcceptingOrders` и `MenuItemAvailable` уже существуют; `RestaurantOrderCancellable` — кандидат. Kitchen Queue и её порядок не являются State `RestaurantOrder`: при ручной перестановке порядок становится данными отдельного aggregate, а не новым state.

## Delivery

Statechart aggregate `Delivery` в `delivery`; реализация — `boundaries/delivery/slices/delivery/domains/delivery/fsm.rs`. Use cases описаны в [delivery README](../boundaries/delivery/README.md); здесь — только модель переходов.

Compound states:

| Compound state | Входит | Смысл |
| --- | --- | --- |
| `ActiveAssignment` | `Assigned`, `PickedUp` | Active Courier Assignment: courier действует по Delivery и видит её order (FR-444); возможна Delivery Failure |
| `Closed` | `Delivered`, `Failed`, `Cancelled` | Terminal states |

`Waiting` — узел верхнего уровня: Courier ещё не закреплён.

```mermaid
stateDiagram-v2
    [*] --> Waiting
    Waiting --> Assigned: Assign [courier]
    state ActiveAssignment {
        Assigned --> PickedUp: Pickup
    }
    state Closed {
        Delivered
        Failed
        Cancelled
    }
    Assigned --> Waiting: Unassign
    PickedUp --> Delivered: Complete
    ActiveAssignment --> Failed: Fail
    Waiting --> Cancelled: Cancel
    Assigned --> Cancelled: Cancel
    Closed --> [*]
```

| source | trigger | guard | target | failure |
| --- | --- | --- | --- | --- |
| `Waiting` | `Assign` | Передан `courier_id` | `Assigned` | `GuardFailed{courier_id_required}` |
| `Waiting` | `Cancel` | — | `Cancelled` | — |
| `Assigned` | `Unassign` | — | `Waiting` | — |
| `Assigned` | `Pickup` | — | `PickedUp` | — |
| `Assigned` | `Cancel` | — | `Cancelled` | — |
| `PickedUp` | `Complete` | — | `Delivered` | — |
| `ActiveAssignment` | `Fail` | — | `Failed` | — |

`Cancel` объявлен на `Waiting` и `Assigned`, а не на compound state: Delivery Cancellation возможна только до Pickup и пересекает границу Active Courier Assignment, а у узла один родитель. Ни `Waiting`, ни `Assigned` отдельно не образуют термина, ради которого стоило бы вводить группу.

Строгие повторы с явным `AlreadyInState`:

| from_state | trigger | Исход |
| --- | --- | --- |
| `Assigned` | `Assign` | `AlreadyInState{Assigned}` |
| `PickedUp` | `Pickup` | `AlreadyInState{PickedUp}` |
| `Delivered` | `Complete` | `AlreadyInState{Delivered}` |
| `Failed` | `Fail` | `AlreadyInState{Failed}` |
| `Cancelled` | `Cancel` | `AlreadyInState{Cancelled}` |

Все остальные пары `state × trigger` дают `InvalidTransition`. В том числе `PickedUp × Cancel` — Cancel Delivery сопоставляет его с бизнес-отказом `AlreadyPickedUp`, а `InvalidTransition` из `Closed` — с `AlreadyClosed`. Хранилище выбирает Delivery с активным назначением по `State::leaves_of(CompoundState::ActiveAssignment)`, loaded aggregate — по `state.is_in(CompoundState::ActiveAssignment)`.

## Payment

Statechart aggregate `Payment` в `payments`; реализация — `boundaries/payments/slices/payment/domains/payment/fsm.py`. Инварианты и use cases описаны в [README payments](../boundaries/payments/README.md); здесь — только модель переходов. Состояния `Payment Operation` (`pending → succeeded | declined`) — отдельная таблица entity и в эту FSM не входят.

Compound states:

| Compound state | Входит | Смысл |
| --- | --- | --- |
| `uncaptured` | `pending`, `authorized` | Деньги ещё не списаны; платёж может завершиться неудачей (`fail`) |
| `closed` | `refunded`, `failed` | Terminal states |

`captured` — leaf верхнего уровня: деньги списаны, возможен только Refund.

```mermaid
stateDiagram-v2
    [*] --> pending: CreatePayment
    state uncaptured {
        pending --> authorized: authorize [provider_reference]
        authorized --> pending: cancel_authorization
    }
    state closed {
        refunded
        failed
    }
    authorized --> captured: capture
    captured --> refunded: refund
    uncaptured --> failed: fail
    closed --> [*]
```

| source | trigger | guard | target | failure |
| --- | --- | --- | --- | --- |
| `pending` | `authorize` | Provider Reference не пустой | `authorized` | `GuardFailed{provider_reference_required}` |
| `authorized` | `cancel_authorization` | — | `pending` | — |
| `authorized` | `capture` | — | `captured` | — |
| `captured` | `refund` | — | `refunded` | — |
| `uncaptured` | `fail` | — | `failed` | — |

Строгие повторы с явным `AlreadyInState`:

| from_state | trigger | Исход |
| --- | --- | --- |
| `pending` | `cancel_authorization` | `AlreadyInState{pending}` |
| `authorized` | `authorize` | `AlreadyInState{authorized}` |
| `captured` | `capture` | `AlreadyInState{captured}` |
| `refunded` | `refund` | `AlreadyInState{refunded}` |
| `failed` | `fail` | `AlreadyInState{failed}` |

Все остальные пары `state × trigger` дают `InvalidTransition`. В том числе `captured × fail` — после Capture платёж не становится `failed`, отрицательный исход оформляется Refund; `captured × cancel_authorization` — списанную сумму возвращает Refund, а не Cancel Authorization.

Требование Provider Reference при восстановлении (`authorized`, `captured`, `refunded`) — инвариант данных aggregate, а не compound state: набор пересекает `uncaptured` и `closed`.

# Kitchen: приготовление Restaurant Order

Документ детализирует ответственность bounded context `restaurants` за работу кухни: доменную модель приготовления, use cases, состояния, бизнес-сценарии и границу с `orders`, `delivery` и `payments`. Это SDD/DDD-описание будущих слайсов. Код, Protobuf/gRPC API и окончательная transition table здесь не фиксируются; концептуальная FSM вынесена в [FSM: Kitchen / Preparation](fsm.md#kitchen--preparation).

Новый ADR не создаётся: документ детализирует уже назначенную `restaurants` ответственность за Acceptance и Preparation ([ADR 2](adr/0002-define-bounded-contexts-and-implementation-languages.md), [README `restaurants`](../boundaries/restaurants/README.md)). Решения, которые выходят за эту детализацию, помечены как **предложение** и собраны в разделе [Предложения](#предложения).

Опорные документы: [Temporal ADR `orders`](../boundaries/orders/docs/adr/0002-use-temporal-to-orchestrate-orders.md) и [ADR 5 orders](../boundaries/orders/docs/adr/0005-order-fulfillment-orchestration.md), [FSM](fsm.md) и [ADR 21](adr/0021-pure-fsm-transitions.md), [Specification Pattern](specification-pattern.md) и [ADR 22](adr/0022-specification-pattern-for-domain-policies.md), [Domain и Integration Events](domain-events.md) и [ADR 25](adr/0025-integration-event-publication.md), Inbox/Outbox ([ADR 11](adr/0011-transactional-inbox-and-idempotent-event-processing.md), [ADR 13](adr/0013-transactional-outbox-for-integration-event-publishing.md)), internal gateway ([ADR 15](adr/0015-использовать-internal-gateway-для-синхронных-вызовов-бизнес-api.md)), SpiceDB ([ADR 18](adr/0018-spicedb-for-authorization.md), [security](security.md)), [Ubiquitous Language](ubiquitous-language.md#restaurants).

## Граница

`restaurants` владеет цепочкой:

```text
Restaurant Order → Kitchen Queue → Preparation → Assembly → Ready for Pickup
```

| Понятие | Владелец | Чем не является |
| --- | --- | --- |
| `Restaurant Order` и его State | `restaurants` | `orders.Order`, `Order State` |
| Kitchen Queue, Preparation, Assembly, Ready for Pickup | `restaurants` | Workflow Stage `orders`, `delivery.Pickup` |
| Общий lifecycle заказа, Cancellation, Completion | `orders` | Кухонный процесс |
| Assignment, Pickup, Delivery Completion | `delivery` | Готовность кухни |
| Authorization, Capture, Refund | `payments` | Финансовые последствия отмены кухни |

`orders` видит только внешние факты кухни — Integration Events через Kafka и Inbox — и синхронный результат команд, которые он сам вызывает через internal gateway. Состояния `Restaurant Order`, Kitchen Queue и Preparation Item не импортируются в `orders` и не становятся его `Order State` или Workflow Stage.

## Модель

### Один aggregate `RestaurantOrder`

**Рабочее допущение MVP:** кухонный lifecycle — это states одного aggregate `RestaurantOrder`. Все Commands, Domain Events и Integration Events адресуют `RestaurantOrder`.

`Kitchen Order` — термин для **роли** `Restaurant Order` в процессе кухни после Acceptance («заказ, над которым работает кухня»). Это не вторая сущность, не отдельный тип в коде и не отдельный документ хранилища. В прежних документах `Kitchen Order`/`KitchenOrder` использовался как имя aggregate — это legacy-имя ([аудит словаря](ubiquitous-language.md#аудит-существующих-имён)).

Обоснование одного aggregate:

- Acceptance и Preparation защищают общий инвариант: кухня готовит ровно принятый состав, и только принятый заказ может быть приготовлен или отменён;
- у решения ресторана и работы кухни один identity, одна связь с `orders.Order` и одна последовательность фактов, которую `orders` читает как один поток;
- текущие SDD не показывают инвариантов, которые жили бы отдельно от Acceptance: нет разбиения одного заказа на независимые кухонные тикеты, объединения нескольких заказов в одну партию или повторного приготовления заказа с новым lifecycle.

Отдельный aggregate `KitchenOrder` оправдан, если появится хотя бы одно из условий: один `Restaurant Order` порождает несколько кухонных единиц работы со своими states (тикеты по станциям, несколько волн подачи); кухня объединяет несколько заказов в одну партию; параллельные изменения позиций станциями дают устойчивые конфликты optimistic concurrency на одном документе; Acceptance и Preparation начинают меняться разными командами с разными требованиями к согласованности. Вопрос остаётся открытым.

### Состав aggregate

| Элемент | Назначение |
| --- | --- |
| `id` | Identity `RestaurantOrder` в `restaurants` |
| `orderId` | Контрактный `Order ID` из `orders`; уникален — один `RestaurantOrder` на один `Order`, повтор `Receive Order Request` не создаёт дубликат |
| `restaurantId` | Ресторан, который принял заказ; resource для authorization |
| `items` | Preparation Items принятого состава |
| `state` | Business state FSM ([FSM](fsm.md#kitchen--preparation)) |
| `priority` | Входной признак порядка в Kitchen Queue; значения не согласованы |
| timeline | Временные метки, перечисленные ниже |
| `version` | Optimistic concurrency, вне FSM |

### Timeline

Минимальный набор для Kitchen Queue, Preparation Delay и будущего SLA:

| Поле | Когда фиксируется | Использование |
| --- | --- | --- |
| `accepted_at` | Acceptance | Порядок очереди, время реакции ресторана |
| `queued_at` | Queue Restaurant Order | Время ожидания в очереди |
| `preparation_started_at` | Start Preparation | Длительность Preparation; граница политики отмены |
| `expected_ready_at` | Устанавливается при Acceptance или постановке в очередь, меняется Report Preparation Delay | Ожидание `orders`/`delivery`, задержки |
| `ready_at` | Mark Ready for Pickup | Фактическая длительность, SLA |

Все значения передаются в domain method от application clock ([FSM: Time и identifiers](fsm.md#time-и-identifiers)); aggregate не читает часы. Время Assembly и terminal outcomes несут `occurred_at` соответствующих событий; отдельные поля timeline для них не вводятся до появления сценария, которому они нужны. Способ расчёта начального `expected_ready_at` — открытый вопрос.

### Preparation Item

`Preparation Item` — позиция `Restaurant Order` в работе кухни: ссылка на Menu Item принятого состава, количество и выбранные options/modifiers. Это не `Menu Item` (текущее предложение меню) и не `orders.Order Item` (позиция оформленного заказа с ценой). Цена кухне не нужна и в Preparation Item не хранится.

**Required Preparation Item** — позиция принятого состава, не исключённая согласованным решением ([Unavailable item](#unavailable-item-после-acceptance)). Только required items участвуют в проверке готовности.

`Kitchen Station` (grill, drinks, desserts, assembly) — доменное понятие места или участка приготовления. В MVP модель станций не вводится; позже Preparation Item может ссылаться на станцию для маршрутизации и capacity.

### Инварианты

- `orderId` уникален в пределах `restaurants`; `menuItemId` с одинаковыми options не повторяется внутри одного заказа (повтор выражается `Quantity`).
- Принять можно только полученный заказ открытого ресторана, все позиции которого доступны (`RestaurantAcceptingOrders`, `MenuItemAvailable`).
- Состав после Acceptance меняется только явным решением по недоступной позиции, а не правкой меню.
- Preparation начинается только для заказа в Kitchen Queue и только со свободным Cook на линии.
- Cook держит не больше одного заказа в Active Preparation; гарантирует хранилище, см. [Kitchen Capacity](#kitchen-capacity).
- **Заказ не становится Ready, пока не готовы все required items** — проверяется aggregate, а не handler или UI. Без item-level model эквивалент на уровне заказа: Ready допустим только после Mark Assembled, которым кухня подтверждает, что все required items приготовлены и собраны.
- Ready не наступает автоматически от завершения последней позиции: Assembly и Mark Ready for Pickup — явные действия кухни.
- Terminal states (`READY`, `REJECTED`, `CANCELLED`, `FAILED`) не меняются изменяющими переходами; повтор даёт `AlreadyInState` или `InvalidTransition`.

## Happy path

```text
Restaurant Order received → accepted → queued → preparation started
→ items prepared → assembled → ready for pickup
```

| Шаг | Бизнес-смысл | Инициатор | State после шага | Факт |
| --- | --- | --- | --- | --- |
| Received | Ресторан получил состав заказа для решения; кухня ещё не обязалась его готовить | Система: Activity `orders` через internal gateway | `RECEIVED` | `RestaurantOrderReceived` (локальный) |
| Accepted | Ресторан обязался приготовить весь состав; кухня получает заказ в работу | Ресторан: сотрудник | `ACCEPTED` | `RestaurantOrderAccepted` |
| Queued | Заказ занял место в Kitchen Queue по согласованной политике; приготовление не начато | Система `restaurants` | `QUEUED` | `RestaurantOrderQueued` (локальный) |
| Preparation started | Кухня начала тратить ресурсы на заказ | Кухня: сотрудник | `PREPARING` | `PreparationStarted` |
| Items prepared | Все required items приготовлены, параллельно по станциям | Кухня: сотрудники станций | `PREPARING` | `PreparationItemCompleted` (только item-level model) |
| Assembled | Позиции собраны в один заказ; позже — упаковка, проверка, sealing | Кухня: сотрудник | `ASSEMBLED` | `RestaurantOrderAssembled` (локальный) |
| Ready for pickup | Заказ можно передать курьеру | Кухня: сотрудник | `READY` | `RestaurantOrderReady` |

В MVP Queue выполняется в той же Unit of Work сразу после Acceptance, поэтому `ACCEPTED` кратковременен. State сохраняется явным, чтобы отложенная постановка в очередь (заказ к времени, capacity) не меняла модель. Assembly в MVP может быть мгновенной операцией: одно действие кухни без промежуточного ожидания, но всё равно отдельный trigger перед Ready.

## Use cases

Все команды кухни выполняются от verified `Subject`, имеющего permission над `restaurant:<restaurantId>`, выводимую из relation `staff` (`restaurant.staff`, [security](security.md#rebac-model)). Роли cook/manager в authorization не моделируются: [Cook](#kitchen-capacity) — единица мощности кухни, а не subject, и команды кухни выполняет subject с `restaurant#operate_kitchen`, а не сам Cook. Команды `orders` выполняет `service` identity worker'а, authorization выполняет `restaurants` ([ADR 18](adr/0018-spicedb-for-authorization.md)). Authorization выполняется до domain operation и не входит в FSM или Specification.

### Commands

| Use Case | Contract name (кандидат) | Actor | Предусловие (State) | Результат | Факт |
| --- | --- | --- | --- | --- | --- |
| **Receive Order Request** | `ReceiveRestaurantOrder` | Activity `orders` | Нет заказа с этим `orderId` | `RECEIVED`; повтор возвращает существующий заказ | — |
| **Accept Order** | `AcceptRestaurantOrder` | Сотрудник ресторана | `RECEIVED` | `ACCEPTED`, затем `QUEUED`; `accepted_at`, `expected_ready_at` | `RestaurantOrderAccepted` |
| **Reject Order** | `RejectRestaurantOrder` | Сотрудник ресторана | `RECEIVED` | `REJECTED` с причиной | `RestaurantOrderRejected` |
| **Queue Restaurant Order** | `QueueRestaurantOrder` | Система `restaurants` | `ACCEPTED` | `QUEUED`, `queued_at`; место определяется политикой очереди | `RestaurantOrderQueued` |
| **Start Preparation** | `StartRestaurantOrderPreparation` | Сотрудник кухни | `QUEUED`; есть свободный Cook на линии | `PREPARING`, `preparation_started_at`, `cook_id`; без свободного Cook — `NoCookAvailable` | `PreparationStarted` |
| **Start Preparation Item** | `StartPreparationItem` | Сотрудник станции | Заказ `PREPARING`, item `PENDING` | Item `PREPARING` | `PreparationItemStarted` |
| **Complete Preparation Item** | `CompletePreparationItem` | Сотрудник станции | Заказ `PREPARING`, item `PREPARING` | Item `READY` | `PreparationItemCompleted` |
| **Mark Assembled** | `MarkRestaurantOrderAssembled` | Сотрудник кухни | `PREPARING`; все required items готовы | `ASSEMBLED` | `RestaurantOrderAssembled` |
| **Mark Ready for Pickup** | `MarkRestaurantOrderReady` | Сотрудник кухни | `ASSEMBLED` | `READY`, `ready_at` | `RestaurantOrderReady` |
| **Report Preparation Delay** | `ReportPreparationDelay` | Сотрудник кухни | `QUEUED`, `PREPARING`, `ASSEMBLED`; новое время позже текущего | `expected_ready_at` изменён; State не меняется | `PreparationDelayed` |
| **Report Preparation Failure** | `ReportPreparationFailure` | Сотрудник кухни | `PREPARING`, `ASSEMBLED` | `FAILED` с причиной | `RestaurantOrderFailed` |
| **Cancel Restaurant Order** | `CancelRestaurantOrder` | Activity `orders` | По стадиям, см. [Cancellation](#cancellation-по-стадиям) | Отменено либо отказ с причиной | `RestaurantOrderCancelled`, если State изменён |
| **Reject After Acceptance** | `RejectRestaurantOrder` | Сотрудник ресторана | `ACCEPTED`, `QUEUED` — только после продуктового решения | `REJECTED` с причиной | `RestaurantOrderRejected` |

Start/Complete Preparation Item существуют только при item-level model ([Preparation Item lifecycle](#preparation-item-lifecycle)); для MVP они не обязательны. Report Preparation Failure добавлен к исходному перечню: FSM содержит `FAILED`, а `orders` ожидает факт неудачи приготовления, поэтому у перехода должен быть явный инициатор.

#### Queue Restaurant Order

Выполняется системой после Acceptance. Место в Kitchen Queue определяется согласованной политикой очереди; алгоритм не выбран ([Kitchen Queue](#kitchen-queue)). Приготовление не начинается, ресурсы кухни не тратятся. В MVP команда исполняется в той же UoW, что и Accept Order: handler вызывает `accept(...)` и `queue(...)` последовательно, aggregate записывает два Domain Event, публикуется только `RestaurantOrderAccepted`.

#### Start Preparation

Кухня берёт заказ из очереди, система назначает ему свободного Cook на линии ([Kitchen Capacity](#kitchen-capacity)). Нет свободного Cook — бизнес-отказ `NoCookAvailable`, заказ остаётся в `QUEUED`. Повтор команды для заказа, уже находящегося в `PREPARING`, даёт `AlreadyInState{PREPARING}` ([FSM](fsm.md#transition-table-и-результат)).

**Политика повтора (предложение для MVP):** kitchen board — общий экран нескольких сотрудников, повторное нажатие и повтор запроса после потери ответа являются нормальным UX. Handler превращает `AlreadyInState{PREPARING}` в успешный no-op: возвращает текущее состояние, не создаёт Domain Event и не публикует `PreparationStarted` повторно. Если заказ уже ушёл дальше (`ASSEMBLED`, `READY`) или в terminal state, FSM возвращает `InvalidTransition`, и UI показывает актуальное состояние. BDD-сценарий повтора остаётся `@pending` до подтверждения политики продуктом.

Текущее допущение реализации: политика не зашита в handler — Start Preparation и Mark Ready for Pickup получают обязательный параметр `onRepeat: "succeed-without-event" | "reject"` без значения по умолчанию, значение выбирает composition root ([design.md change `implement-restaurants-application-layer`](../openspec/changes/archive/2026-10-25-implement-restaurants-application-layer/design.md#допущения-по-открытым-продуктовым-вопросам)). Продуктовый вопрос остаётся открытым.

#### Mark Assembled

Кухня подтверждает, что все required items приготовлены и собраны в один заказ. При item-level model guard проверяет, что каждый required item в `READY`; иначе `GuardFailed{RequiredItemsNotPrepared}` с перечнем незавершённых позиций. Без item-level model действие кухни само является утверждением о готовности позиций.

#### Mark Ready for Pickup

Допустим только из `ASSEMBLED`: инвариант готовности уже выполнен на шаге Assembly. Из `PREPARING` команда отклоняется (`InvalidTransition`) — заказ не может стать Ready, минуя Assembly. Повтор в `READY` — `AlreadyInState{READY}`; политика повтора такая же, как у Start Preparation.

#### Report Preparation Delay

Кухня сообщает новое `expected_ready_at`. Результат — изменённое ожидаемое время; State не меняется. Guard: новое значение позже текущего `expected_ready_at` и позже `now`. Причина задержки — необязательная категория ([Preparation Delay](#preparation-delay)). Сообщение о более раннем времени задержкой не является; нужна ли отдельная команда уточнения ETA — открытый вопрос.

#### Cancel Restaurant Order

Синхронная команда `orders`: compensation или cancellation Activity Order Fulfillment Workflow → internal gateway → API `restaurants` → handler. Ответ выражает фактический результат по стадии:

| Результат | Когда | Integration Event |
| --- | --- | --- |
| `Cancelled{stage}` | Отмена применена; `stage` — State в момент отмены | `RestaurantOrderCancelled` |
| `Cancelled{stage}` (повтор) | Заказ уже `CANCELLED` — `AlreadyInState`, возвращается сохранённый результат | Не публикуется повторно |
| `CancellationRejected{reason}` | Политика стадии запрещает отмену или заказ уже в другом terminal state (`READY`, `REJECTED`, `FAILED`) | Нет |

Текущие допущения реализации ([design.md change `implement-restaurants-application-layer`](../openspec/changes/archive/2026-10-25-implement-restaurants-application-layer/design.md#допущения-по-открытым-продуктовым-вопросам)): политика отмены во время Preparation — обязательный параметр handler `cancellationDuringPreparation: "allow" | "reject"` без значения по умолчанию, передаётся в guard FSM; Cancel до Receive возвращает `NotFoundError`, tombstone не вводится. Продуктовые вопросы остаются открытыми.

Отказ — business result, а не technical error: Activity не повторяет его как retryable failure ([ADR 5 orders](../boundaries/orders/docs/adr/0005-order-fulfillment-orchestration.md#activities)). Команда идемпотентна по `orderId` и логическому шагу; gateway retry допустим только после согласования этой политики ([ADR 15](adr/0015-использовать-internal-gateway-для-синхронных-вызовов-бизнес-api.md)). Отмена `restaurants` не завершает `Order`: полный процесс отмены, компенсации и финансовые последствия решает `orders` через Temporal.

#### Reject After Acceptance

Инициатива ресторана отказаться от уже принятого заказа до начала приготовления (сломалось оборудование, закончился ингредиент). Возможность — отдельное продуктовое решение; в MVP переходы `ACCEPTED/QUEUED → REJECTED` не разрешены. Если решение будет принято, используется тот же trigger `Reject` и тот же факт `RestaurantOrderRejected` с признаком стадии; `orders` должен уметь обработать его после Acceptance ([Сверка с Temporal](#сверка-с-сигналами-temporal)). Текущая реализация возвращает `InvalidTransition` для `ACCEPTED/QUEUED × Reject` ([design.md change `implement-restaurants-application-layer`](../openspec/changes/archive/2026-10-25-implement-restaurants-application-layer/design.md#допущения-по-открытым-продуктовым-вопросам)).

### Queries

| Use Case | Contract name (кандидат) | Actor | Результат | Модель |
| --- | --- | --- | --- | --- |
| **Get Kitchen Queue** | `GetKitchenQueue` | Сотрудник ресторана | Заказы `QUEUED` в порядке очереди с `expected_ready_at` | Projection Kitchen Queue |
| **Get Restaurant Order** | `GetRestaurantOrder` | Сотрудник ресторана; авторизованный внутренний actor | Состав, State, timeline одного заказа | Read model `restaurants` |
| **List Active Preparations** | `ListActivePreparations` | Сотрудник ресторана | Заказы `PREPARING`, `ASSEMBLED` с прогрессом позиций и задержками | Read model kitchen board |
| **List Ready Orders** | `ListReadyOrders` | Сотрудник ресторана | Заказы `READY`, ожидающие передачи курьеру, с информацией о курьере при наличии | Read model kitchen board |
| **Get Cook Pool** | `GetCookPool` | Сотрудник ресторана | Pool Load: размер, на линии, занято, в очереди; Cook с текущим заказом | Read model по `cooks` и `restaurant_orders` |

Queries читают собственные read models `restaurants`; kitchen board не восстанавливает aggregate на каждый запрос. Read models обновляются из Domain Events той же UoW или из собственных Integration Events через Inbox — выбор механизма относится к реализации.

## Kitchen Queue

Kitchen Queue — бизнес-очередь кухни: порядок, в котором принятые заказы ожидают начала Preparation. Это не Kafka topic, не Temporal task queue и не job queue.

**MVP:** очередь — projection по заказам в `QUEUED` с ключами `accepted_at` и `priority`. Она не является aggregate: порядок вычисляется из данных `RestaurantOrder`, а не хранится. Start Preparation не обязан брать первый заказ — кухня выбирает заказ, очередь показывает рекомендованный порядок.

Scheduling-алгоритм не выбирается. Открытые варианты: FIFO по `accepted_at`; priority orders; зависимость от courier ETA (начинать так, чтобы Ready совпал с прибытием курьера); ручной порядок.

**Следствие ручного порядка:** если кухня может переставлять заказы, порядок становится хранимым состоянием с собственным инвариантом (одна позиция на заказ, согласованность при конкурентных перестановках). Тогда очередь становится aggregate — например `KitchenQueue` на ресторан, — а `RestaurantOrder` перестаёт быть единственным источником порядка. Это открытый вопрос.

## Preparation Item lifecycle

Item-level model необязательна для MVP. **Вариант A (рекомендуемый MVP):** Preparation Items — только состав; прогресс отслеживается на уровне заказа, Mark Assembled подтверждает готовность всех позиций. **Вариант B:** у каждой позиции собственный lifecycle внутри aggregate `RestaurantOrder`.

```text
PENDING → PREPARING → READY
негативные: FAILED, CANCELLED
```

| State | Смысл |
| --- | --- |
| `PENDING` | Позиция ожидает начала |
| `PREPARING` | Станция готовит позицию |
| `READY` | Позиция готова к Assembly |
| `FAILED` | Позиция испорчена или не может быть приготовлена; требует решения |
| `CANCELLED` | Позиция исключена из required согласованным решением |

Набор негативных states не окончательный. `FAILED` не завершает заказ автоматически: кухня может повторить приготовление (`FAILED → PREPARING`) или запустить решение по недоступной позиции ([Unavailable item](#unavailable-item-после-acceptance)). Пока required item в `FAILED`, Assembly невозможна.

Позиции готовятся параллельно и независимо, Assembly их объединяет:

```text
burger  PENDING → PREPARING → READY ┐
fries   PENDING → PREPARING → READY ├→ Mark Assembled → Mark Ready for Pickup
drink   PENDING → READY ────────────┘
```

Переходы позиций допустимы только пока заказ в `PREPARING`. Lifecycle позиции — часть модели aggregate `RestaurantOrder`, а не отдельная FSM другого aggregate ([FSM](fsm.md#kitchen--preparation)).

## Unavailable item после acceptance

После Acceptance выясняется, что позицию невозможно приготовить. Варианты — открытые вопросы:

| Вариант | Последствие |
| --- | --- |
| Cancel всего заказа | Простейший; `orders` компенсирует платёж полностью |
| Substitution | Нужны правила равноценной замены и, вероятно, согласие Customer |
| Удаление item с компенсацией | Item → `CANCELLED`; `orders`/`payments` выполняют частичный возврат, которого нет в текущей модели `payments` |
| Ожидание решения Customer | Новое ожидание в workflow `orders` и новый Signal; кухня держит заказ |

**MVP (кандидат на отдельную спецификацию):** до начала приготовления — Reject After Acceptance, если продукт его разрешит; после начала — Report Preparation Failure с причиной `ITEM_UNAVAILABLE`, заказ в `FAILED`. Частичные варианты не входят в MVP, потому что требуют partial refund и участия Customer.

## Cancellation по стадиям

| Стадия | States | Бизнес-смысл | Политика (кандидат MVP) |
| --- | --- | --- | --- |
| Before acceptance | `RECEIVED` | Кухня ничего не обещала; типичная компенсация после timeout Acceptance | Отменить |
| Before preparation | `ACCEPTED`, `QUEUED` | Ресурсы не потрачены | Отменить |
| During preparation | `PREPARING`, `ASSEMBLED` | Ресурсы потрачены, еду, вероятно, придётся списать | Отдельная policy `RestaurantOrderCancellable`; кандидат — разрешить и вернуть `stage`, чтобы `orders` решил финансовые последствия |
| After ready | `READY` | Кухня завершила работу; физически не отменяется | `CancellationRejected{ALREADY_READY}`; `orders` решает, что делать с готовым заказом и деньгами |

Вне-state условия политики (окно отмены, доля потраченных ресурсов) выражаются Specification ([Specification Pattern](specification-pattern.md)); допустимость по State — только transition table. `restaurants` сообщает фактический результат; полный процесс отмены решает `orders`.

## Preparation Delay

Причины: перегрузка кухни, оборудование, сложный заказ, нехватка персонала, позиция готовится дольше обычного. Домен не обязан знать детали: результат — `expected_ready_at changed`. Категория причины допустима как необязательное значение для аналитики, без влияния на переходы. Модель ETA (как считается ожидаемое время и как задержки влияют на курьера) — отдельная задача.

## Courier interaction

Кухня не назначает курьера и не управляет Delivery. Допустима информационная projection в read models kitchen board: courier assigned, ETA, arrived — из Integration Events `delivery` через Inbox. Projection не меняет ownership: `delivery` остаётся владельцем Assignment и Pickup, `RestaurantOrder` не зависит от модели `delivery`, данные курьера не участвуют в переходах FSM. Нужен ли courier ETA кухне для порядка очереди — открытый вопрос.

## Kitchen Capacity

Мощность кухни — Cook Pool ресторана: change [`add-cook-and-courier-pools`](../openspec/changes/add-cook-and-courier-pools/design.md).

- **Cook** — повар ресторана с именем. Не subject и не relation `staff`: `staff` решает, кто вправе выполнять команды кухни, Cook — кто готовит заказ. Новый Cook вне линии.
- **Cook Pool** — невыбывшие Cook ресторана. Hire Cook, Put Cook On Line, Take Cook Off Line, Retire Cook требуют `restaurant#manage_cooks` (owner и staff).
- **Занятость** выводится из заказов: Cook занят, пока Restaurant Order с его `cook_id` в `PREPARING` или `ASSEMBLED`, и освобождается на `READY`, `FAILED` или `CANCELLED` без отдельной команды. Отдельного флага в Cook нет, поэтому переходы заказа не меняют второй aggregate. `cook_id` остаётся в заказе как история.
- **Назначение** автоматическое: Start Preparation выбирает свободного Cook на линии с самым ранним последним назначением и в той же UoW обновляет его `lastAssignedAt`. Конкурентные назначения одного Cook дают write conflict; инвариант «один Cook — один активный заказ» дополнительно держит unique partial index ([ADR 0002 restaurants](../boundaries/restaurants/docs/adr/0002-mongodb-collections-and-document-boundaries.md)).
- **Снять с линии** можно только свободного Cook (`CookBusy`), **убрать из пула** — только вне линии (`CookOnLine`).
- **Acceptance от линии не зависит.** При пустой линии заказы принимаются и ждут в `QUEUED`; если никто не встанет на линию, таймер ожидания Preparation workflow `orders` ведёт к бизнес-ветке отмены. Kitchen board показывает пустую линию предупреждением.
- **Pool Load** — `busy / on_line` и Kitchen Queue как backlog (Get Cook Pool).

Не решено: ручное назначение Cook, расчёт `expected_ready_at` по очереди и числу Cook на линии, связь Cook с Kitchen Station.

## События

### Domain Events (кандидаты)

Domain Event записывает domain method явно; FSM не создаёт событие на каждый переход ([ADR 21](adr/0021-pure-fsm-transitions.md#domain-methods-и-events)).

| Domain Event | Domain method | Публикуется | Integration Event |
| --- | --- | --- | --- |
| `RestaurantOrderReceived` | `receive` | Нет (1 → 0) | — |
| `RestaurantOrderAccepted` | `accept` | Да | `RestaurantOrderAccepted` |
| `RestaurantOrderRejected` | `reject` | Да | `RestaurantOrderRejected` |
| `RestaurantOrderQueued` | `queue` | Нет | — |
| `PreparationStarted` | `startPreparation` | Да | `PreparationStarted` |
| `PreparationItemStarted` | `startItem` | Нет | — |
| `PreparationItemCompleted` | `completeItem` | Нет | — |
| `RestaurantOrderAssembled` | `markAssembled` | Нет | — |
| `RestaurantOrderReady` | `markReady` | Да | `RestaurantOrderReady` |
| `PreparationDelayed` | `reportDelay` | Да | `PreparationDelayed` |
| `RestaurantOrderCancelled` | `cancel` | Да | `RestaurantOrderCancelled` |
| `RestaurantOrderFailed` | `reportFailure` | Да | `PreparationFailed` |

`RestaurantOrderReceived` и `RestaurantOrderAccepted` добавлены к исходному списку кандидатов: публикуемый факт должен быть записан aggregate ([Domain Events](domain-events.md#цепочка-публикации)). Список не окончательный.

### Integration Events

Публикуются только факты, нужные `orders` и `delivery`, через Transactional Outbox ([ADR 25](adr/0025-integration-event-publication.md)). Payload state-carrying и без PII; окончательный состав фиксирует SDD контракта.

| Integration Event | Нужен | Payload (кандидат) |
| --- | --- | --- |
| `RestaurantOrderAccepted` | `orders`: продолжить Fulfillment | `order_id`, `restaurant_order_id`, `restaurant_id`, `accepted_at`, `expected_ready_at` |
| `RestaurantOrderRejected` | `orders`: business failure | `order_id`, `reason`, `stage` (до/после Acceptance) |
| `PreparationStarted` | `orders`: Record Preparation Started | `order_id`, `preparation_started_at`, `expected_ready_at` |
| `PreparationDelayed` | `orders`: ожидание Preparation; потенциально `delivery` | `order_id`, `expected_ready_at`, `previous_expected_ready_at` |
| `RestaurantOrderReady` | `orders`: Record Order Ready; потенциально `delivery` | `order_id`, `ready_at` |
| `RestaurantOrderCancelled` | `orders`: подтверждение результата компенсации | `order_id`, `stage`, `preparation_started` |
| `PreparationFailed` | `orders`: компенсация | `order_id`, `reason`, `stage` |

Цепочка — по [Domain и Integration Events](domain-events.md): domain method → `PullEvents()` → решение handler → Outbox port в той же UoW → infrastructure mapper → `nomnom.restaurants.v1.*` → Kafka. Partition key — кандидат `order_id`, чтобы `orders` получал факты одного `Order` по порядку; решение — вместе с topics.

### Сверка с сигналами Temporal

Ожидания `orders` зафиксированы в [ADR 5 orders](../boundaries/orders/docs/adr/0005-order-fulfillment-orchestration.md) и разделе [Order Fulfillment Workflow](../boundaries/orders/README.md#входящие-события): имя Signal совпадает с именем Integration Event.

| Integration Event | Signal | Use case `orders` | Поведение workflow |
| --- | --- | --- | --- |
| `RestaurantOrderAccepted` | `RestaurantOrderAccepted` | Confirm Order | Завершает ожидание Acceptance |
| `RestaurantOrderRejected` | `RestaurantOrderRejected` | Reject Order | До Acceptance — ветка отказа; после Acceptance и до Pickup — Saga, а не игнорирование |
| `PreparationStarted` | `PreparationStarted` | Record Preparation Started | Фиксирует начало Preparation |
| `PreparationDelayed` | `PreparationDelayed` | — (только workflow) | Продлевает бизнес-таймер ожидания Preparation; предел — продуктовое решение |
| `RestaurantOrderReady` | `RestaurantOrderReady` | Record Order Ready | Завершает ожидание Preparation |
| `RestaurantOrderCancelled` | Не используется для ветвления | — | Подтверждает синхронный результат compensation Activity `CancelRestaurantOrder`; Saga не перезапускает |
| `PreparationFailed` | `PreparationFailed` | — (только workflow) | Saga |

Открытым остаётся только продуктовое решение `restaurants`, допустим ли отказ после Acceptance; workflow `orders` готов его принять.

## UI projection: kitchen board

Kitchen board — projection, а не FSM один в один.

| Колонка | States | Примечание |
| --- | --- | --- |
| `NEW` | `RECEIVED`, `ACCEPTED`, `QUEUED` | Отдельно отмечаются заказы, ожидающие решения, и заказы в очереди |
| `PREPARING` | `PREPARING`, `ASSEMBLED` | Имя Cook; прогресс позиций при item-level model; задержки по `expected_ready_at` |
| `READY` | `READY` | До факта Pickup из `delivery`; данные курьера — информационные |

Шапка kitchen board показывает Cook Pool: линию, занятых, очередь и список Cook с действиями линии. `REJECTED`, `CANCELLED`, `FAILED` не показываются в рабочих колонках и доступны в истории. Customer-facing Status (`Preparing` в [UI](ui.md)) формирует `orders`/BFF из своих данных, а не из колонок kitchen board.

## Temporal и persistence

Temporal находится в `orders`; кухонный процесс не является workflow. Ожидание Preparation — человеческая работа и state `RestaurantOrder`, а не открытая Activity.

```text
orders workflow → Activity → internal gateway → restaurants: Receive Order Request
kitchen: Accept → Queue → Start Preparation → … → Mark Ready for Pickup
restaurants Outbox → Kafka → orders consumer → Inbox → handler → Signal workflow
```

Собственный долгоживущий orchestration кухни (таймеры напоминаний, авто-эскалация задержек) — отдельное архитектурное решение, не принятое здесь.

MongoDB, направление: aggregate `RestaurantOrder` — один document; `Restaurant` и `Menu` — отдельные documents; Kitchen Queue и read models kitchen board — projections. Один документ на ресторан вместе с меню и заказами не используется: заказы растут без ограничения и меняются с другой частотой. Минимальная модель в [README `restaurants`](../boundaries/restaurants/README.md#минимальная-доменная-модель) держит `MenuItem` внутри aggregate `Restaurant`; это граница aggregate, а не обязательно граница документа. Acceptance только читает открытость ресторана и доступность позиций и не меняет их атомарно с `RestaurantOrder`. State `RestaurantOrder` опирается на single-document atomicity; изменение вместе с Outbox или Inbox выполняется Unit of Work с MongoDB transaction ([Unit of Work](unit-of-work.md#restaurants--mongodb)). Финальные document boundaries и indexes определяются по consistency и access patterns.

## BDD кандидаты

Сценарии используют бизнес-язык, без Mongo, Kafka и FSM ([testing](testing.md)). Feature-файлы создаются в `acceptance/` соответствующих use cases после утверждения acceptance criteria.

```gherkin
Feature: Kitchen queue

  Scenario: Accepted order enters kitchen queue
    Given a restaurant order is received by an open restaurant
    When the restaurant accepts the order
    Then the order is in the kitchen queue
    And preparation has not started

Feature: Start preparation

  Scenario: Kitchen starts preparing a queued order
    Given a restaurant order is queued
    When the kitchen starts preparation
    Then the order is being prepared

  @pending
  Scenario: Kitchen starts preparation of an order already being prepared
    Given a restaurant order is being prepared
    When the kitchen starts preparation again
    Then the order is still being prepared
    And no new preparation start is reported

Feature: Order ready for pickup

  Scenario: Order becomes ready
    Given all required items are prepared
    And the order is assembled
    When the kitchen marks the order ready
    Then the order is ready for pickup

  Scenario: Order cannot become ready too early
    Given some required items are not prepared
    When the kitchen marks the order ready
    Then the request is rejected
    And the order is not ready for pickup
```

Сценарий повтора Start Preparation остаётся `@pending` до подтверждения политики `AlreadyInState`.

## Открытые вопросы

- Нужен ли отдельный aggregate `KitchenOrder` и при каких условиях ([Модель](#один-aggregate-restaurantorder)).
- Нужна ли item-level model в MVP.
- Нужна ли Assembly как отдельный длительный шаг (`ASSEMBLING` + `ASSEMBLED`) или достаточно мгновенной операции.
- Может ли ресторан отказаться после Acceptance и до какой стадии. Текущее допущение реализации — `InvalidTransition`.
- Политика unavailable item: cancel, substitution, удаление с компенсацией, ожидание Customer.
- Порядок Kitchen Queue: FIFO, priority, courier ETA, ручной порядок; ручная перестановка делает очередь aggregate.
- Kitchen Capacity: влияние Cook Pool на `expected_ready_at` и ручное назначение Cook ([Kitchen Capacity](#kitchen-capacity)).
- Когда начинается поиск курьера относительно Preparation.
- Нужен ли кухне courier ETA.
- Как рассчитывается начальное `expected_preparation_time` / `expected_ready_at`.
- Семантика дубликата команды: no-op или ошибка для Start Preparation и Mark Ready for Pickup. Текущее допущение реализации — обязательный параметр handler `onRepeat` без значения по умолчанию.
- Политика отмены во время Preparation и после Ready, включая списание еды. Текущее допущение реализации — обязательный параметр handler `cancellationDuringPreparation` без значения по умолчанию.
- Нужно ли `restaurants` подтверждение передачи заказа курьеру (state после `READY`) или достаточно факта Pickup из `delivery`.
- Какие события публиковать, их окончательные имена и потребитель `delivery` для `RestaurantOrderReady` и `PreparationDelayed`.
- Грамматика имён: `PreparationStarted`, `PreparationDelayed`, `PreparationFailed` не следуют форме `<Aggregate><Fact>`; кандидаты `RestaurantOrderPreparationStarted` и подобные — решить при создании контракта.
- Cancel до Receive: Activity отмены может прийти раньше, чем повтор Receive Order Request создаст заказ; нужен ли tombstone, запрещающий последующее создание. Текущее допущение реализации — `NotFoundError` без tombstone.

Текущие допущения реализации описаны в [design.md change `implement-restaurants-application-layer`](../openspec/changes/archive/2026-10-25-implement-restaurants-application-layer/design.md#допущения-по-открытым-продуктовым-вопросам); они не закрывают вопросы выше.

## Предложения

Архитектурные предложения, не принятые этим документом:

1. **State `ASSEMBLED` вместо концептуального `ASSEMBLING`.** Mark Assembled фиксирует завершение Assembly, а `RestaurantOrderAssembled` — его факт; state после команды — результат, а не процесс. `ASSEMBLING` вводится вместе с командой начала сборки, если Assembly станет длительным шагом со своей станцией.
2. **Явный `RECEIVED` в FSM** до Acceptance — состояние ожидания решения уже описано use case Receive Order Request.
3. **Use case Report Preparation Failure** как инициатор `FAILED` и `PreparationFailed`.
4. **Политика повтора** Start Preparation и Mark Ready for Pickup — успешный no-op без нового события.
5. **Для `orders`** — принято в [ADR 5 orders](../boundaries/orders/docs/adr/0005-order-fulfillment-orchestration.md): сигналы названы по Integration Events, `PreparationStarted`/`PreparationDelayed`/`PreparationFailed` и `RestaurantOrderRejected` после Acceptance обрабатываются workflow, `RestaurantOrderCancelled` — подтверждение синхронного результата. `PreparationDelayed` и `PreparationFailed` не отражаются в `Order` отдельными use cases: это входы orchestration, а не Fulfillment Facts.

# 5. Order Fulfillment Orchestration

Date: 2026-10-25

## Status

Accepted

Уточняет [2. Использовать Temporal для оркестрации заказов](0002-use-temporal-to-orchestrate-orders.md)

Связан с [4. Использование CQRS в orders](0004-использование-cqrs-в-orders.md) и общесистемными ADR [11. Transactional Inbox and Idempotent Event Processing](../../../../docs/adr/0011-transactional-inbox-and-idempotent-event-processing.md), [13. Transactional Outbox for Integration Event Publishing](../../../../docs/adr/0013-transactional-outbox-for-integration-event-publishing.md), [15. Internal gateway](../../../../docs/adr/0015-использовать-internal-gateway-для-синхронных-вызовов-бизнес-api.md), [17. Cloud Native Application Runtime](../../../../docs/adr/0017-cloud-native-application-runtime.md) и [18. SpiceDB for Authorization](../../../../docs/adr/0018-spicedb-for-authorization.md)

## Context

[ADR 2](0002-use-temporal-to-orchestrate-orders.md) выбирает Temporal, но не определяет границу его применения, identity workflow, безопасный старт после оформления `Order`, протокол входящих событий, компенсации и разделение состояния `Order` и workflow. Без этого реализация может хранить заказ в Event History, использовать Temporal для CRUD или event delivery, повторять финансовые и компенсационные эффекты при at-least-once доставке.

Application-слой `orders` уже видит workflow только через порт `slices/order/applications/fulfillment` (`Starter`, `Notifier`, `CancellationRequester`). Этот ADR задаёт контракт Temporal-адаптера за портом. Требования и сценарии — в [спецификации `order-fulfillment-orchestration`](../../../../openspec/specs/order-fulfillment-orchestration/spec.md).

## Decision

### Scope Temporal

Temporal применяется только к многошаговому процессу одного `Order`, который содержит внешние зависимости, ожидания, retries, бизнес-таймеры или компенсации и должен переживать перезапуск worker. Операция, укладывающаяся в `request → transaction → response`, выполняется без workflow.

Temporal не используется для:

- CQRS queries — клиентские чтения (`GetOrder`, `ListCustomerOrders`) обслуживает read model по [ADR 4](0004-использование-cqrs-в-orders.md);
- локальных транзакций и хранения агрегатов — `Order` хранится в PostgreSQL `orders`;
- доставки событий между bounded contexts — это Outbox → Kafka → Inbox ([ADR 11](../../../../docs/adr/0011-transactional-inbox-and-idempotent-event-processing.md), [ADR 13](../../../../docs/adr/0013-transactional-outbox-for-integration-event-publishing.md));
- authorization — решения принимает SpiceDB в защищённом use case владельца ([ADR 18](../../../../docs/adr/0018-spicedb-for-authorization.md));
- универсальных фоновых заданий и job queue.

### Identity и топология

Один Order Fulfillment Workflow на один логический `Order`, без child workflows. Workflow ID — `order:<order_id>`, namespace — отдельный на окружение, task queue — `orders`. Повторный старт для того же `Order` адресует существующий workflow (`USE_EXISTING`). Будущая декомпозиция допустима только с сохранением этого внешнего identity.

Процесс — отдельная бизнес-возможность со своей моделью, поэтому он выделен в слайс `fulfillment` (OpenSpec change `extract-order-fulfillment-slice`): правила принимает доменная модель `slices/fulfillment/domains/fulfillment` без Temporal SDK, а workflow, Activities и client — адаптер `slices/fulfillment/infrastructure/temporal`. Порт workflow остаётся в слайсе `order` (`slices/order/applications/fulfillment`), `order` не импортирует `fulfillment`. Слайс `fulfillment` использует `order` только через готовые use cases: их собирает `orderdi.FulfillmentModule`, а адаптеры и порты этих use cases `fulfillment` не видит. Если workflow начнёт охватывать несколько слайсов помимо `order`, он переносится в `boundaries/orders/infrastructure/temporal/` по [общесистемному ADR 6](../../../../docs/adr/0006-ddd-structure-inside-vertical-slices.md#входы-в-application).

### Старт

Единственный путь: Place Order атомарно сохраняет `Order` и `OrderPlaced` в Outbox и Temporal не вызывает; consumer `orders.startfulfillment` (группа `orders`, `orders.v1.order-events`) получает собственное событие через Kafka и Inbox, в той же UoW читает `Order` и после commit выполняет `SignalWithStart(order:<order_id>, OrderPlaced, USE_EXISTING)`. Порядок start и первого Signal становится несущественным, а crash между commit заказа и стартом не теряет процесс. Дубликат Inbox тоже выполняет `SignalWithStart`: так лечится crash между commit Inbox и стартом, а повтор в работающий workflow отсекает дедупликация по `event_id`.

Для `Order` в `completed` или `cancelled` сигнал не отправляется: история закрытого workflow могла уже уйти из namespace по retention, и `SignalWithStart` создал бы его заново (redrive DLQ, сброс offset). Ошибка Temporal — transient: сообщение не подтверждается и доставляется повторно, в DLQ не уходит. Собственные `OrderCompleted` и `OrderCancelled` того же топика consumer `orders` подтверждает явным no-op.

Синхронный старт из Place Order (после commit или до него) не используется: он давал второй путь старта и терял процесс при crash между commit и стартом. В окне между commit заказа и стартом workflow Request Order Cancellation отвечает `UNAVAILABLE`, клиент повторяет запрос.

### Входящие события и идемпотентность

Факты `restaurants`, `delivery` и `payments` проходят путь `Kafka → orders consumer → Inbox → application handler → Signal/Update`. Соседние контексты не вызывают Temporal напрямую.

Дедупликация двухуровневая: Inbox по `(consumer, event_id)` защищает локальные эффекты consumer; workflow хранит обработанные `event_id` и превращает повтор Signal RPC в no-op. Имя Signal совпадает с именем Integration Event; перевод `fulfillment.Kind` в имя Signal выполняет адаптер.

Положительное событие, которое текущий Workflow Stage не ожидает, логируется и игнорируется без падения workflow. Отрицательные факты (`RestaurantOrderRejected`, `PreparationFailed`, `DeliveryFailed`, `PaymentFailed`) в незавершённом workflow молча не игнорируются: они ведут в Saga с диагностикой.

### Signal, Update, Query

- Signal — асинхронный факт без ответа.
- Update — команда, которой нужен результат validation относительно Workflow Stage; клиентская отмена — Update `CancellationRequested`. Допустимость Cancellation решает домен `Order`, компенсации — workflow.
- Query — только техническая диагностика.
- Temporal `CancelWorkflow` — аварийная операция и не моделирует Order Cancellation.

### Activities

Workflow детерминирован и не выполняет gRPC, HTTP, DB, Kafka, SpiceDB или иных side effects — только через Activities. Activity получает сериализуемый input с `order_id`, логическим шагом и idempotency key; ключ финансовых и компенсационных операций — `order_id + step`, не зависит от attempt и run ID.

Activity, меняющая `orders`, вызывает handler слайса, который атомарно пишет `Order` и Outbox; прямой publish в Kafka запрещён. Данные для запроса к соседу Activity получает query [Get Fulfillment Order](../../slices/order/applications/getfulfillmentorder/README.md): он читает сам `Order`, а не проекцию, и возвращает view без доменных типов, поэтому агрегат и его repository за пределы слайса `order` не выходят. Границу проверяет depguard. Вызов API другого контекста идёт через `INTERNAL_GATEWAY_ADDR` от `service` identity worker'а ([ADR 15](../../../../docs/adr/0015-использовать-internal-gateway-для-синхронных-вызовов-бизнес-api.md), [ADR 18](../../../../docs/adr/0018-spicedb-for-authorization.md)); целевой контекст авторизует эту identity сам.

Каждая Activity имеет явные Start-To-Close timeout и Retry Policy; длительные — heartbeat. Business rejection возвращается результатом, не technical retry.

### Приоритет и fairness task queue

Workflow и Activities одного `Order` делят task queue `orders`. Когда задач больше, чем слотов worker'а, порядок выдачи задают [Task Queue Priority и Fairness](https://docs.temporal.io/develop/task-queue-priority-fairness) Temporal; уже выданные worker'у задачи они не меняют.

- **Fairness key — `restaurant_id`.** Каждое заведение работает независимо, поэтому ключ — ресторан, а не сеть или Market. Ключ задаётся при `SignalWithStart` из `Order`, который consumer читает в UoW старта; workflow tasks (Signals, Update `CancellationRequested`) и Activities наследуют его. Ресторан в пике — ASAP или запланированные заказы одного окна — получает свою долю worker'а и не задерживает задачи остальных. Веса у ресторанов одинаковые (1.0), переопределения весов не используются. Старт без `restaurant_id` — ошибка.
- **Priority key — по Activity**, строго между уровнями, fairness — внутри уровня:
  - `1` — ждёт покупатель или его деньги: `AuthorizePayment`, `CancelPaymentAuthorization`, `RefundPayment`, `ConfirmSlotReservation`;
  - `2` — ждут кухня или курьер, компенсации: `RequestRestaurantOrder`, `QueueRestaurantOrder`, `CreateDelivery`, `CapturePayment`, `CancelRestaurantOrder`, `CancelDelivery`, `FailOrCancelOrder`, `RecordPaymentAuthorization`;
  - `3` — учёт, планирование наперёд и очистка: `CompleteOrder`, `PlanSchedule`, `ReleaseSlotReservation`.

  Activity без уровня не регистрируется: worker не стартует. Workflow tasks отдельного приоритета не имеют — Update отмены идёт с приоритетом workflow, ускоряются её компенсации.
- **Лимит частоты на fairness key не вводится.** Он задерживал бы задачи ресторана сверх лимита, включая оплату его покупателей, а приём ASAP-заказов по загрузке не ограничивается ([Kitchen](../../../../docs/kitchen.md)). Priority и fairness не отклоняют задачи и admission control не являются.

Fairness включается на сервере: dynamic config `matching.enableFairness=true` для namespace (в kind и testcontainers — флаг dev server); Priority включён по умолчанию. Fairness действует внутри партиции task queue и гарантирована только для задач одной Worker Deployment Version: при выкатке runs старой и новой версии делят worker'ы без общего круга, priority при этом действует.

### Ожидания и таймеры

Ожидание ресторана и доставки — Signal плюс Temporal timer; polling запрещён. Timer выражает продуктовый срок и ведёт к бизнес-ветке (cancellation, compensation, старт запланированного заказа). Workflow-level timeout не задаётся. `PreparationDelayed` продлевает таймер ожидания Preparation.

Courier Assignment workflow ждёт без срока (FR-709): поиском курьера целиком владеет Assignment Workflow `delivery` ([локальный ADR 2 `delivery`](../../../delivery/docs/adr/0002-temporal-для-назначения-курьера.md)), курьер рано или поздно найдётся, а оператор доставку не выполнит. Перепланирования доставки по сроку, решения оператора (`OperatorDecision`, stage `waiting_operator`) и RPC `ReplanDelivery` нет.

### Запланированный заказ

Order с Delivery Window (OpenSpec change `add-scheduled-orders`, FR-415, FR-416, FR-678–FR-682) идёт своим маршрутом. Ветка выбирается по окну во входе workflow (`Input.DeliveryWindow` из `OrderPlaced`); ASAP-маршрут не меняется.

```text
AuthorizePayment → RecordPaymentAuthorization → ConfirmSlotReservation → PlanSchedule
→ RequestRestaurantOrder(window, planned_ready_at) → ждать RestaurantOrderAccepted
→ stage scheduled: timer replan_at → PlanSchedule (тот же planned_ready_at)
                   timer start_at → QueueRestaurantOrder
                   timer courier_request_at → CreateDelivery
→ дальше обычный маршрут: Assignment, Preparation, Pickup, Capture, Delivery Completion
```

- Place Order резервирует окно в `restaurants` до своей транзакции (hold); workflow подтверждает резервацию после записи оплаты. `ConfirmSlotReservation` с ответом `slot_unavailable` (hold истёк, окно заполнилось) — бизнес-результат: Saga отменяет Authorization и завершает `Order` кодом `slot_unavailable` без запроса ресторана.
- План считает use case Plan Schedule слайса `order` (чистая domain-политика `order.PlanSchedule` над UTC-моментами) по базовому времени ресторана и оценке доставки Market. Activity `PlanSchedule` возвращает план, workflow хранит его в Event History; `Order` план не хранит. Первый план фиксирует `planned_ready_at`, контрольная точка `replan_at` один раз пересчитывает `start_at` и `courier_request_at` с тем же `planned_ready_at`. План, сделанный уже после своей контрольной точки, не пересчитывается.
- Таймеры — те же Temporal timers главного цикла: модель отдаёт ближайший момент плана в `NextDeadline`, наступивший момент делает шаг due (`Expire`), и цикл исполняет его как Activity. Момент в прошлом означает немедленный шаг, пересчёт плана заменяет таймеры следующим циклом. Polling не вводится.
- Stage `scheduled` публикуется Search Attribute от `RestaurantOrderAccepted` до `QueueRestaurantOrder`.
- Deadline Acceptance — как у ASAP. Deadline Preparation отсчитывается от `QueueRestaurantOrder`; Assignment срока не имеет; в stage `scheduled` deadline `Policy` не действует. `PreparationDelayed` до `QueueRestaurantOrder` отклоняется (`not queued`).
- `RequestRestaurantOrder` запланированного заказа возвращает `restaurant_order_id`; workflow передаёт его в `QueueRestaurantOrder` (idempotency key `order_id + queue`). ASAP-маршрут результат этой Activity не читает.
- Отмену запланированного заказа покупатель видит до `window_start − CANCELLATION__SCHEDULED_CUTOFF` (cancellation policy по данным `Order`); Update `CancellationRequested` в stage `scheduled` ведёт к `CancelRestaurantOrder`, `ReleaseSlotReservation`, `CancelPaymentAuthorization`.

### Saga

Компенсации — orchestration-based Saga. Workflow ведёт журнал подтверждённых шагов и вызывает только применимые compensation Activities (`CancelDelivery`, `CancelRestaurantOrder`, `ReleaseSlotReservation`, `CancelPaymentAuthorization`, `RefundPayment`), каждая со стабильным idempotency key, timeout, retry policy и telemetry. Резервация окна запланированного заказа существует с Place Order, поэтому журнал содержит её с начала: при отказе оплаты, отмене или неудаче до Pickup workflow вызывает `ReleaseSlotReservation` (ключ `order_id + release_slot`); после Pickup окно отработало и не освобождается, неподтверждённая резервация (`slot_unavailable`) — тоже.

Ресторан запрашивается только после оплаты: маршрут `AuthorizePayment → RecordPaymentAuthorization → RequestRestaurantOrder → …`. Шаг `RecordPaymentAuthorization` вызывает use case слайса `order`, который записывает в `Order` Fulfillment Fact `payment_authorized` с моментом решения (время Workflow) и переводит его в `paid`; повтор Activity — no-op, компенсации у шага нет — факт остаётся в `Order` как история. Restaurant Order создаёт только `RequestRestaurantOrder` (RPC `ReceiveOrderRequest`); `restaurants` не создаёт его из `OrderPlaced`. Иначе кухня видела неоплаченный заказ: её факты могли опередить шаг `RequestRestaurantOrder` (workflow отклонял их и падал по таймауту приёмки), а при отказе оплаты Saga не отменяла Restaurant Order, и кухня могла его приготовить. Поэтому при отказе Authorization Restaurant Order не существует и `CancelRestaurantOrder` не нужен. Цена — кухня видит заказ позже на время Authorization.

### State ownership

| Что | Владелец |
| --- | --- |
| Order State (`placed`, `paid`, `confirmed`, `preparing`, `ready`, `picked_up`, `rejected`, `delivery_failed`, `delivered`, `cancelled`, `completed`), журнал Fulfillment Facts, инварианты | `Order` в PostgreSQL `orders` |
| Workflow Stage: стадия, результаты шагов, обработанные `event_id`, таймеры, журнал compensation | Temporal Event History |
| Доставка интеграционных событий | Kafka + Outbox/Inbox |
| Клиентские чтения | CQRS read model |

У каждого контекста свой заказ и свой автомат: `RestaurantOrder` у кухни, `Delivery` у доставки, `Order` у покупателя. `Order` не копирует чужие states, а переводит факты соседей на язык покупателя: `preparing`, `ready`, `picked_up`, `rejected`, `delivery_failed`, `delivered`. Поиск и назначение курьера — states `Delivery`, в `Order` они не дублируются: `courier_assigned` остаётся записью журнала, а `CourierAssignment` нужен для доступа курьера и порядка событий; самого курьера BFF читает из `delivery`.

Order State — результат шагов Fulfillment, Workflow Stage — ожидание следующего: `preparing` значит «приготовление началось», а ждёт ли процесс готовности и с каким таймером, знает только workflow. Раньше прогресс хранился только фактами, а статус BFF вычислял из их набора: это был неявный автомат в другом сервисе. Теперь положение заказа — один state, а факты — журнал времени и причин (модель переходов — [FSM](../../../../docs/fsm.md#order)).

Факты `restaurants` и `delivery` приходят из разных топиков, поэтому переходы монотонны: факт может перепрыгнуть несообщённый шаг, а отставший только дописывается в журнал. Строгий порядок давал бы `InvalidTransition`, откат Inbox и повторы на нормальной перестановке событий. Порядок шагов по-прежнему задаёт workflow, `Order` его не проверяет.

Отрицательный факт и доставка сразу меняют state: покупатель не должен видеть «везут» после провала доставки. Компенсации и код причины задаёт Saga, поэтому `rejected` и `delivery_failed` промежуточные и завершаются `cancelled`, а `delivered` — `complete`.
| Authorization | SpiceDB в use case владельца |

Workflow не читает БД. Stage публикуется низкокардинальным Search Attribute и не становится Order State.

### Runtime

API, Kafka consumer и Temporal worker — управляемые процессы по [ADR 17](../../../../docs/adr/0017-cloud-native-application-runtime.md). Worker при shutdown прекращает poll, даёт Activities завершиться, закрывает clients и flush telemetry. Crash worker не является business failure и не запускает compensation.

### Versioning workflow code

Код workflow версионируется Worker Versioning (Worker Deployment Versions), а не patch-ветками (`workflow.GetVersion`) в коде. Worker объявляет версию `DeploymentOptions{UseVersioning, Version{DeploymentName, BuildID}}`, а каждый workflow — `VersioningBehavior` при регистрации; поведение по умолчанию у worker не задаётся, и workflow без явного поведения не регистрируется.

- `PINNED` — run живёт на версии worker'а, где стартовал; код не патчится.
- `AUTO_UPGRADE` — run переезжает на текущую версию, поэтому изменение кода требует patching.
- Upgrade on Continue-as-New (preview) — долгий `PINNED` run переходит на новую версию при следующем Continue-As-New. Preview-возможности Temporal допустимы.

| Workflow | Длительность | Стратегия |
| --- | --- | --- |
| Order Fulfillment (сага заказа, ASAP) | минуты–часы | `PINNED` |
| Order Fulfillment запланированного заказа | часы–дни (до `SCHEDULED_SLOT__MAX_HORIZON`) | тот же workflow, `PINNED`; ветка под patch `scheduled-route` |
| GroupCart | до недели | `PINNED` + Upgrade on Continue-as-New |

Запланированный заказ — ветка того же Order Fulfillment Workflow, а не отдельный workflow: identity `order:<order_id>` и Saga общие. Ветка введена под Temporal patch: `workflow.GetVersion("scheduled-route", DefaultVersion, 1)` вызывается только для входа с окном, поэтому ASAP-маршрут не пишет новых команд, и histories, начатые до ветки, воспроизводятся без изменений; history без маркера идёт ASAP-маршрутом. `PINNED` держит запланированный заказ на версии worker'а до окна, поэтому версия с открытыми запланированными заказами живёт до `MAX_HORIZON`; patch нужен, чтобы этот код мог безопасно выполнять и replay'ить histories предыдущей версии (откат, перевод на `AUTO_UPGRADE`). Следующее изменение кода ветки делается следующим patch, а не правкой ветки. GroupCart пока не реализован; таблица задаёт его стратегию. Replay tests в CI обязательны для каждого workflow с `AUTO_UPGRADE`; для ветки `scheduled-route` test environment проверяет, что history без маркера идёт ASAP-маршрутом.

Версии разворачивает [Temporal Worker Controller](https://github.com/temporalio/temporal-worker-controller) — одинаково в kind и в production. Release `orders-worker` описывает `WorkerDeployment` и `Connection` вместо `Deployment`. Контроллер создаёт `Deployment` на каждый Build ID (хеш pod template), передаёт поду `TEMPORAL_DEPLOYMENT_NAME` и `TEMPORAL_WORKER_BUILD_ID`, регистрирует версию и делает её текущей. Без этих переменных worker не стартует. Rollout `AllAtOnce`: новые заказы сразу идут на новую версию, начатые доживают на своей. Версию без открытых `PINNED` runs (Drained) контроллер масштабирует в 0 через 1 час и удаляет через 24 часа. Возврат к версии, которая была текущей в последний час, контроллер выполняет сразу; runs, начатые на откатываемой версии, на ней и завершаются.

### Отложенные продуктовые решения

Этот ADR не выбирает следующие решения; каждое принимается отдельным SDD change до реализации соответствующего шага:

- продуктовые timeout Order Acceptance, Preparation, Pickup и Delivery Completion и предел продления через `PreparationDelayed`;
- момент Capture и условия Refund вместо отмены Authorization (Authorization идёт до запроса ресторана, см. [Saga](#saga));
- параллельный запуск Assignment относительно Acceptance или Preparation;
- policy каждого события, пришедшего после завершения workflow;
- допускает ли `restaurants` отказ после Acceptance (workflow готов его принять);
- Continue-As-New threshold и retention обработанных `event_id`.

Логическая схема `Order placed → Authorize → Payment recorded → Acceptance → Assignment → Preparation → Pickup → Capture → Delivery Completion → Completion` — карта coordination points, а не утверждённая последовательность.

## Consequences

Старт, входящие события, внешние эффекты и компенсации безопасны при at-least-once повторах и crash, а ответственность `payments`, `restaurants` и `delivery` не переходит в `orders`. `Order` остаётся source of truth и доступен без Temporal. У старта один путь — consumer собственного `OrderPlaced`; кухня получает только оплаченные заказы, и оплата видна в `Order` фактом `payment_authorized`.

Цена: два уровня дедупликации; старт workflow зависит от Debezium и Kafka, поэтому при их задержке workflow, а с ним и кухня, стартует позже; кухня ждёт Authorization; рост Event History на длинных заказах (потребуется Continue-As-New). Версионирование `PINNED` убирает патчи и replay tests из саги, но каждое изменение pod template — новая версия, а старая держит поды, пока на ней есть открытые заказы. Production зависит от Temporal Worker Controller и Temporal Server с Worker Deployments (не ниже 1.29.1) и включённой Task Queue Fairness. Priority и fairness видны только при очереди задач: при свободных слотах worker'а порядок не меняется.

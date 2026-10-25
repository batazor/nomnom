# Design

## Context

См. `proposal.md` — Why. Локальный ADR 2 уже выбирает Temporal для долгоживущей orchestration в `orders`, ADR 4 выбирает CQRS, а общесистемные ADR 11 и 13 задают Inbox/Outbox и at-least-once delivery. ADR 15 требует проводить синхронные бизнес-вызовы Activity через internal gateway, ADR 18 — использовать собственную `service` identity и оставить authorization владельцу use case, ADR 17 — запускать API, consumer и Temporal worker как управляемые cloud-native процессы.

Change `implement-orders-application-layer` уже реализовал доменную модель `Order` и handlers всех Commands и Queries поверх in-memory адаптеров. Handlers видят workflow только через порт `slices/order/applications/fulfillment` (`Starter`, `Notifier`, `CancellationRequester`); `placeorder` вызывает `Starter` после commit и публикует Domain Event `OrderPlaced` через Outbox. Temporal client/worker, workflow code, Activities, Kafka consumers и PostgreSQL ещё не реализованы. Поэтому документ задаёт контракт Temporal-адаптера за этим портом и не фиксирует преждевременно protobuf, таблицы, Kafka topics или SDK package layout.

## Goals / Non-Goals

**Goals:**

- связать lifecycle одного `Order` с ровно одним durable orchestration identity;
- сделать старт, входящие события, внешние эффекты и компенсации безопасными при at-least-once повторах и crash;
- сохранить ответственность и язык bounded contexts;
- отделить Order State, Workflow Stage, event delivery и authorization;
- дать будущим слайсам проверяемые правила выбора Signal, Update, Activity, timer и CQRS query.

**Non-Goals:**

- окончательно определить последовательность Authorization/Capture и параллелизм Courier Assignment;
- задать продуктовые значения timeout, число попыток replan или operator escalation SLA;
- спроектировать API `payments`, `restaurants`, `delivery`, Kafka schemas и topics;
- определить retention Event History, Continue-As-New policy и Temporal deployment topology;
- реализовать исходный код, зависимости, manifests или observability dashboards.

## Decisions

### Один верхнеуровневый workflow без child workflows

На первом этапе один Order Fulfillment Workflow управляет процессом целиком. Workflow ID — `order:<order_id>`, namespace разделён по окружениям, task queue — `orders`; child workflows не используются.

Это делает identity процесса очевидной, упрощает адресацию Signals и диагностику. Child workflows добавили бы отдельные lifecycle и failure boundaries до появления доказанной потребности. Будущая декомпозиция допустима только с сохранением внешнего identity и совместимой migration long-running histories.

### SignalWithStart закрывает гонку «событие до старта»

Основной bootstrap:

```text
Create Order transaction
  ├─ persist Order
  └─ persist OrderPlaced in Outbox
          ↓
Kafka → orders consumer → Inbox transaction → application handler
          ↓
SignalWithStart(order:<order_id>, OrderPlaced, USE_EXISTING)
```

PostgreSQL и Temporal не образуют общей транзакции. Outbox гарантирует, что зафиксированный `OrderPlaced` не потеряется, Inbox убирает повтор Kafka delivery, а стабильный Workflow ID и `SignalWithStart` делают порядок start/signal несущественным. Синхронный start после commit допустим как latency optimization, но не становится единственной гарантией и повторяется с тем же ID.

Альтернатива — стартовать workflow до commit — отклонена: workflow мог бы наблюдать несуществующий `Order`. Единственный post-commit RPC без `OrderPlaced` отклонён: crash между commit и RPC потерял бы процесс.

### Workflow хранит orchestration facts, Order хранит бизнес-состояние

Workflow хранит Workflow Stage — сериализуемый orchestration progress: текущую стадию, известные результаты шагов, набор обработанных `event_id`, активные timers и журнал выполненных шагов, необходимых для compensation. `Order` остаётся source of truth для Order State (`placed`, `confirmed`, `cancelled`, `completed`), Fulfillment Facts и доменных инвариантов.

Workflow не читает БД напрямую. Изменение `Order` выполняется Activity/application use case; его результат записывается в Event History. Клиентские чтения обслуживает CQRS. Низкокардинальная stage (`waiting_restaurant`, `waiting_courier`, `compensating` и подобные) публикуется Search Attribute для Temporal UI, но не становится новым `OrderStatus`.

Альтернатива — считать Temporal единственным состоянием заказа — отклонена: она ломает владение агрегатом, CQRS и возможность обслуживать запросы независимо от Temporal Query API.

### Входящие события проходят две идемпотентные границы

```text
Kafka(event_id)
  → orders consumer
  → Transactional Inbox
  → application handler
  → Signal/Update(event_id)
  → workflow dedupe + state validation
```

Inbox защищает локальные эффекты consumer от повторной Kafka delivery. Он не доказывает exactly-once для Signal RPC: timeout возможен после применения Signal. Поэтому workflow сохраняет обработанные `event_id` и повтор превращает в no-op. Новое событие в неожиданной stage логируется и игнорируется, чтобы at-least-once и reordered delivery не делали workflow failed.

Имя Signal совпадает с именем Integration Event, которое его вызвало; отдельный словарь сигналов и маппинг «событие → сигнал» не вводятся. Application-порт оперирует нейтральным `fulfillment.Kind`, а Temporal-адаптер переводит его в имя события. Список развивается вместе с контрактами событий; исходный набор:

| Signal / Update | Источник | Application use case `orders` | Принимается в Workflow Stage |
| --- | --- | --- | --- |
| `OrderPlaced` (`SignalWithStart`) | `orders` | Place Order | Старт; повтор — no-op |
| `PaymentAuthorized`, `PaymentFailed` | `payments` | — (подтверждение результата Activity) | Ожидание Authorization; позже — no-op по `event_id` |
| `RestaurantOrderAccepted` | `restaurants` | Confirm Order | Ожидание Acceptance |
| `RestaurantOrderRejected` | `restaurants` | Reject Order | Любая stage до Pickup: до Acceptance — ветка отказа, после Acceptance — Saga |
| `PreparationStarted` | `restaurants` | Record Preparation Started | После Acceptance |
| `PreparationDelayed` | `restaurants` | — (только workflow) | Ожидание Preparation: продлевает бизнес-таймер |
| `PreparationFailed` | `restaurants` | — (только workflow) | Ожидание Preparation: Saga |
| `RestaurantOrderReady` | `restaurants` | Record Order Ready | После Acceptance |
| `CourierAssignedToDelivery` | `delivery` | Record Courier Assignment | Ожидание Assignment и позже (повторное назначение) |
| `DeliveryPickedUp` | `delivery` | Record Pickup | После Ready |
| `DeliveryCompleted` | `delivery` | Record Delivery Completion | После Pickup |
| `DeliveryFailed` | `delivery` | Record Delivery Failure | После создания Delivery: Saga |
| `CancellationRequested` (Update) | клиент через `orders` | Request Order Cancellation | Любая незавершённая stage; ответ — принят или бизнес-отказ |
| `OperatorDecision` | оператор через `orders` | — | Эскалация Assignment |

Отрицательные факты (`RestaurantOrderRejected`, `PreparationFailed`, `DeliveryFailed`, `PaymentFailed`) не попадают под правило «неожиданная stage → игнорировать», пока workflow не завершён: они либо ведут в Saga, либо, если stage их не допускает, переводят workflow в эскалацию с диагностикой. Тихо игнорируются только повторы и положительные факты, уже не влияющие на маршрут. `RestaurantOrderCancelled` и `PaymentAuthorizationCancelled`/`PaymentRefunded` подтверждают результат компенсирующей Activity, который workflow получил синхронно, и не запускают ветку Saga повторно.

Use cases отражения `PreparationDelayed` и `PreparationFailed` в `Order` не требуются: это не Fulfillment Facts `Order`, а входы orchestration; consumer передаёт их workflow через Inbox без изменения агрегата.

Прямой вызов Temporal из `restaurants`, `delivery` или `payments` отклонён: он связывает boundaries с runtime `orders`, обходит Kafka/Inbox и распределяет знание об identity workflow.

### Signal, Update и Query выбираются по требуемой семантике

- Signal — внешнее асинхронное событие, которому не нужен синхронный ответ workflow.
- Update — команда, которой нужны acceptance, validation или результат относительно текущей orchestration stage; кандидат для клиентского `CancelOrder`.
- Query — только диагностическое чтение ephemeral Workflow Stage, если оно действительно нужно.
- CQRS read model — `GetOrder`, `ListOrders` и другие продуктовые запросы.

Temporal `CancelWorkflow` остаётся технической аварийной операцией. Он не моделирует Order Cancellation, потому что обходит доменное решение и compensation plan.

### Activities являются границей внешнего эффекта

Workflow вызывает только Activities. Activity получает сериализуемый input с `order_id`, логическим step, operation id/idempotency key, необходимыми идентификаторами и deadline-related параметрами. Result выражает успешный ответ, business rejection или типизированную non-retryable ошибку; transport failure остаётся retryable согласно policy.

Для финансовых операций ключ `order_id + step` стабилен между Activity attempts и workflow runs. Тот же принцип применяется к изменениям `orders` и compensation effects. Activity, меняющая `orders`, выполняет application use case, который атомарно пишет business state и Outbox; прямой Kafka publish запрещён.

Синхронный вызов API другого boundary идёт через `INTERNAL_GATEWAY_ADDR` с identity конкретного worker service account. Gateway retry допускается только для методов с согласованной idempotency policy; Temporal retry не отменяет необходимость идемпотентности целевого API.

### Ожидания моделируются Signals и бизнес-таймерами

Ресторан и delivery публикуют результаты через Kafka, а workflow ждёт Signal с Temporal timer. Polling отклонён: он создаёт периодическую нагрузку, ухудшает latency и дублирует event integration.

Timer выражает продуктовый срок и ведёт к бизнес-ветке: replan, escalation, cancellation или compensation. Workflow-level timeout не задаётся, потому что он оборвал бы Saga без доменного результата. Длительные Activities используют heartbeat, но человеческое ожидание или Preparation не удерживают Activity открытой — это состояние workflow.

### Compensation использует журнал подтверждённых шагов

Workflow регистрирует логически завершённые эффекты и при отказе вызывает только применимые compensation Activities. Типовые операции: `CancelRestaurantOrder`, `CancelPaymentAuthorization`, `RefundPayment`. Compensation имеет собственный стабильный idempotency key, timeout, retry policy и telemetry.

Rollback транзакции между boundaries невозможен, поэтому Saga обеспечивает семантическое исправление, а не атомарный откат. Business rejection не повторяется как technical failure; он выбирает следующую ветку Saga.

### Начальная логическая схема остаётся уточняемой

Базовый маршрут:

```text
Order created → Authorize payment → Request Order Acceptance
→ Wait (accepted | rejected | timeout) → Create Delivery / wait Courier Assignment
→ Wait Preparation → Wait Pickup → Capture payment
→ Wait Delivery Completion → Order Completion
```

Это карта coordination points, а не окончательная последовательность. Момент Authorization/Capture, необходимость предварительной проверки, параллельный запуск Courier Assignment и конкретные компенсации должны быть уточнены отдельными SDD use cases до реализации соответствующих слайсов.

### Worker следует Cloud Native Runtime Contract

API, Kafka consumer и Temporal worker могут быть отдельными процессами и имеют собственные composition roots. Worker прекращает poll новых tasks при shutdown, даёт Activities завершиться в SDK-supported пределах, закрывает clients/resources и flush telemetry. Liveness не зависит от Temporal Server; readiness сообщает способность экземпляра принимать новую работу без синхронного probe внешней зависимости.

Crash worker не является business failure и не запускает compensation сам по себе: Temporal повторно доставляет task и восстанавливает workflow через replay.

## Risks / Trade-offs

- [Event History и набор обработанных `event_id` растут на очень длинном Order] → задать измеримые лимиты и Continue-As-New policy до production, сохраняя dedupe window и внешний Workflow ID contract.
- [Activity и gateway могут оба повторять вызов] → разрешать gateway retry только для методов с согласованной идемпотентностью и тестировать неизвестный результат.
- [Позднее событие приходит после terminal state] → каждый event use case документирует ignore или отдельный compensation flow; результат логируется и измеряется.
- [Неверная классификация business rejection вызывает retry storm] → использовать типизированные результаты и non-retryable error classes с contract tests.
- [Несовместимое изменение workflow code ломает replay] → применять Temporal versioning/migration strategy перед первым production deployment и проверять replay histories в CI.
- [Search Attributes получают высокую cardinality] → публиковать только stage; `order_id` уже находится в Workflow ID и не используется как metric label.
- [SpiceDB недоступен во время защищённой Activity] → целевой use case fail closed; Activity применяет bounded technical retry, не impersonate исходного пользователя.

## Migration Plan

Изменение пока только документирует будущую систему, поэтому data migration и rollback runtime не требуются. После согласования документы применяются в следующем порядке: новый локальный ADR, подробный раздел README, затем отдельные SDD changes для конкретных шагов и только после них реализация workflow/Activities.

Первый production rollout должен включать versioned workflow type, namespace/task queue configuration, worker deployment, replay tests и canary. Откат worker допустим только к версии, совместимой с уже записанными Event Histories; удаление workflow type или очереди до завершения/миграции histories запрещено.

## Open Questions

- Продуктовые значения timeout для Order Acceptance, Courier Assignment, OperatorDecision, Preparation, Pickup и Delivery Completion.
- Точная последовательность Authorization/Capture и условия Refund вместо отмены Authorization.
- Какие шаги Courier Assignment запускаются параллельно с Order Acceptance или Preparation.
- Event-specific policy для Signal, пришедшего после terminal workflow.
- Допускает ли `restaurants` отказ (`RestaurantOrderRejected`) после Acceptance и на сколько `PreparationDelayed` может продлить ожидание Preparation.
- Continue-As-New threshold и retention обработанных `event_id` после появления измеримого профиля Event History.

# Design

## Context

Контракт orchestration задан [ADR 5](../../../boundaries/orders/docs/adr/0005-order-fulfillment-orchestration.md) и спецификацией `order-fulfillment-orchestration`. Handlers `orders` реализованы поверх in-memory адаптеров; workflow они видят через порт `slices/order/applications/fulfillment`. gRPC API `payments`, `restaurants` и `delivery` ещё не опубликованы, Temporal Server в окружении не развёрнут, worker-процесса нет.

## Goals / Non-Goals

**Goals:**

- исполнимый workflow одного `Order` с полным маршрутом ADR 5, проверяемый без Temporal Server;
- Activities, вызывающие handlers `orders` и узкие client-порты соседних контекстов;
- реализация порта `fulfillment` на Temporal client с маппингом ошибок.

**Non-Goals:**

- выбор продуктовых timeout, момента Capture и параллельности Assignment — значения `Policy` остаются placeholders;
- worker-процесс, конфигурация Temporal client, DI, Helm, регистрация Search Attribute;
- gRPC-клиенты соседних контекстов и Kafka consumers, включая consumer `OrderPlaced`;
- replay-тесты на записанных histories — до первого production rollout по ADR 5.

## Decisions

### Package и зависимость

Всё размещается в `slices/order/infrastructure/temporal/fulfillorder`: workflow использует только слайс `order`. Файл `workflow.go` не импортирует домен, handlers и application-порты; Activities и client-адаптер импортируют их. Зависимость — `go.temporal.io/sdk` v1.50.0 (последний стабильный релиз), тесты — его `testsuite` и `mocks`.

### Workflow как реактивный цикл

Signals и Update меняют только сериализуемое состояние workflow (флаги фактов, обработанные `event_id`, решения). Главный цикл после каждого изменения или срабатывания deadline выполняет следующий допустимый шаг через Activity:

```text
start → AuthorizePayment ─declined→ FailOrCancelOrder(failure)
      → RequestRestaurantOrder → wait RestaurantOrderAccepted (acceptance deadline)
      → CreateDelivery → wait CourierAssignedToDelivery (assignment deadline → replan ×N → OperatorDecision)
        ∥ wait PreparationStarted / RestaurantOrderReady (preparation deadline, PreparationDelayed продлевает)
      → wait DeliveryPickedUp (pickup deadline) → CapturePayment
      → wait DeliveryCompleted (delivery deadline) → CompleteOrder
```

Activities вызываются только из главного цикла, поэтому порядок команд детерминирован; signal-обработчики не выполняют side effects. Deadlines — абсолютные моменты workflow-времени, цикл ждёт `AwaitWithTimeout` до ближайшего.

Workflow Stage выводится из флагов: `authorizing_payment`, `waiting_acceptance`, `preparing`, `waiting_operator`, `waiting_pickup`, `delivering`, `completing`, `compensating`, `completed`, `cancelled`. При `Policy.PublishStage` stage публикуется keyword Search Attribute `OrderFulfillmentStage`; в тестах и окружениях без зарегистрированного атрибута флаг выключается.

### Policy

`Policy` передаётся во входе workflow и фиксируется в Event History, поэтому изменение конфигурации не влияет на запущенные workflow. `DefaultPolicy()` содержит placeholders: acceptance 10m, assignment 15m, 2 replan, operator 30m, preparation 90m с продлением не больше 60m, pickup 60m, delivery 120m. Продуктовые значения выбираются отдельным SDD change.

### Входящие Signals

Payload — `Fact{EventID, OccurredAt, Reason, ExpectedReadyAt}`. Повтор `event_id` — no-op. Допустимость по stage:

| Signal | Допустим, если | Иначе |
| --- | --- | --- |
| `RestaurantOrderAccepted` | Restaurant Order запрошен | игнор с log |
| `PreparationStarted`, `RestaurantOrderReady` | Acceptance получен | игнор с log |
| `PreparationDelayed` | Acceptance получен, готовности нет | игнор с log |
| `CourierAssignedToDelivery` | Delivery создана | игнор с log |
| `DeliveryPickedUp` | курьер назначен | игнор с log |
| `DeliveryCompleted` | Pickup получен | игнор с log |
| `RestaurantOrderRejected` | до Pickup | после Pickup — log и эскалация не нужна: еда у курьера |
| `PreparationFailed` | до готовности | игнор с log |
| `DeliveryFailed` | Delivery создана, Completion нет | игнор с log |
| `OperatorDecision` | stage `waiting_operator` | игнор с log |
| `OrderPlaced`, `PaymentAuthorized`, `PaymentFailed` | всегда | подтверждения; только дедупликация |

Во время `completing`/`compensating` все Signals только логируются.

### Cancellation Update

Update `CancellationRequested` с `RequestID` в качестве Update ID. Validator отклоняет запрос при stage `completing`, `compensating` или после Pickup — ошибкой типа `CancellationRefused`; повтор уже принятого `RequestID` принимается. Handler только фиксирует запрос; компенсации выполняет главный цикл, итог — `FailOrCancelOrder(cancellation, customer_requested)`.

### Activities

Идемпотентный ключ — `order:<order_id>:<step>`, где step стабилен (`authorize_payment`, `capture_payment`, `replan_delivery_2` …) и не зависит от attempt. Activity загружает `Order` через repository, строит запрос client-порта из snapshot (сумма, ресторан, позиции, адрес) и возвращает бизнес-результат `Decision{Approved, Reason}` для финансовых операций. `application.ErrInvalidInput` и `order.ErrOrderNotFound` становятся non-retryable `ApplicationError`; остальные ошибки повторяются.

Options: Start-To-Close 30s, Retry Policy 1s → ×2 → 1m без ограничения попыток. Все Activities — короткие RPC, heartbeat не требуется.

### Saga

Журнал шагов: `payment_authorized`, `payment_captured`, `restaurant_order_requested`, `delivery_created` и признаки закрытия на стороне владельца (`restaurant_closed` после отказа или PreparationFailed, `delivery_closed` после DeliveryFailed). Компенсации в обратном порядке:

1. `CancelDelivery`, если Delivery создана и не закрыта;
2. `CancelRestaurantOrder`, если Restaurant Order запрошен, не закрыт и Pickup не произошёл;
3. `RefundPayment`, если был Capture, иначе `CancelPaymentAuthorization`, если была Authorization.

Затем `FailOrCancelOrder` с видом итога и кодом причины: `payment_declined`, `restaurant_rejected`, `restaurant_acceptance_timeout`, `preparation_failed`, `preparation_timeout`, `courier_assignment_exhausted`, `pickup_timeout`, `delivery_failed`, `delivery_timeout`, `payment_capture_declined` (failure) или `customer_requested`, `operator_decision` (cancellation).

### Client-адаптер

| Порт | Вызов Temporal | Ошибки |
| --- | --- | --- |
| `Starter.Start` | `ExecuteWorkflow`, `USE_EXISTING`, `REJECT_DUPLICATE` | уже завершённый workflow — успех |
| `StartFromOrderPlaced` | `SignalWithStartWorkflow(OrderPlaced)` с теми же политиками | уже завершённый workflow — успех |
| `Notifier.Notify` | `SignalWorkflow` с именем по `Kind` | `NotFound` → `ErrWorkflowNotFound` |
| `CancellationRequester` | `UpdateWorkflow`, Update ID = `RequestID`, ожидание Completed | `CancellationRefused` → `ErrCancellationRefused`; `NotFound` → `ErrWorkflowNotFound` |

## Risks / Trade-offs

- [Placeholders Policy примут за продуктовые сроки] → `DefaultPolicy` и README явно помечают их; значения меняются отдельным SDD.
- [Search Attribute не зарегистрирован] → workflow task падает; публикация выключаема флагом, регистрация входит в change развёртывания Temporal.
- [Нет replay-тестов] → обязательны до production rollout; первая версия workflow не использует `GetVersion`.
- [Client-порты соседей — интерфейсы без реализаций] → worker не запускается до gRPC-клиентов; маршрут проверяется unit-тестами.

## Migration Plan

Новый код без потребителей в runtime: `cmd/orders` и DI не меняются. Подключение — отдельным change worker-процесса.

## Open Questions

- Продуктовые значения `Policy` и порядок Authorization/Capture (ADR 5).
- Поведение при отказе Capture после Pickup: сейчас — failure с отменой Authorization.

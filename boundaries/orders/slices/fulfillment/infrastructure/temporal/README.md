# Temporal-адаптер fulfillment

Temporal-адаптер процесса Order Fulfillment по [ADR 5](../../../../docs/adr/0005-order-fulfillment-orchestration.md) и [спецификации `order-fulfillment-orchestration`](../../../../../../openspec/specs/order-fulfillment-orchestration/spec.md). SDK — `go.temporal.io/sdk` v1.50.0. Go package — `temporal`; внешние импортёры дают ему alias (`fulfillmenttemporal`), потому что SDK тоже называется `temporal`.

Правила процесса принимает доменная модель [`domains/fulfillment`](../../domains/fulfillment) слайса [`fulfillment`](../../README.md): адаптер переводит Signals и Update в её вызовы, исполняет её решения (`Step`) как Activities и таймеры и публикует её `Stage` как Search Attribute. Время в модель передаётся из `workflow.Now`.

| Файл | Содержание |
| --- | --- |
| `contract.go` | Workflow ID `order:<order_id>`, task queue `orders`, имена Signals/Update/Activities, payloads, коды причин; `Outcome`, `Stage` и коды причин — alias'ы типов модели |
| `policy.go` | `Policy` — deadlines модели во входе workflow плюс переключатель `PublishStage`; `DefaultPolicy` содержит placeholders, а не продуктовые сроки |
| `workflow.go` | Главный цикл: исполняет `Next` модели как Activity, ждёт `NextDeadline`, вызывает `Expire`, публикует `Stage`, исполняет план `Compensate` |
| `signals.go` | Signals → `Apply`/`Decide` модели и журнал непринятых фактов; Update `CancellationRequested`: validator — `AdmitCancellation`, handler — `AcceptCancellation` |
| `activities.go` | Activities: загрузить `Order` → построить запрос → вызвать порт соседа или use case → классифицировать ошибку; client-порты `Payments`, `Restaurants`, `Delivery` и их DTO — в [`applications/neighbours`](../../applications/neighbours/neighbours.go) |
| `client.go` | Реализация порта [`fulfillment`](../../../order/applications/fulfillment/README.md) слайса `order` на Temporal client: `Start` — `SignalWithStartWorkflow(order:<order_id>, OrderPlaced)` (`AlreadyStarted` — успех), `Notify`, Update отмены |
| `searchattribute.go` | Проверка и регистрация Search Attribute `OrderFulfillmentStage` |
| `register.go` | Регистрация workflow и Activities в worker |

Workflow не импортирует домен `Order` и handlers: Signals и Update меняют только состояние модели, Activities вызываются из главного цикла. Тесты адаптера проверяют интеграцию (Signals доходят до модели, Activities вызываются по её решениям с прежними именами и idempotency key, Search Attribute публикуется) и сквозные сценарии завершения, отмены и компенсации; правила проверяют unit-тесты модели.

## Маршрут

```text
AuthorizePayment → RecordPaymentAuthorization → RequestRestaurantOrder → [RestaurantOrderAccepted] → CreateDelivery
→ [CourierAssignedToDelivery ∥ RestaurantOrderReady] → [DeliveryPickedUp] → CapturePayment
→ [DeliveryCompleted] → CompleteOrder
```

| Deadline (`Policy`) | По истечении |
| --- | --- |
| `AcceptanceTimeout` | `restaurant_acceptance_timeout` |
| `PreparationTimeout` (+ `PreparationDelayed` до `MaxPreparationExtension`) | `preparation_timeout` |
| `PickupTimeout` | `pickup_timeout` |
| `DeliveryTimeout` | `delivery_timeout` |

Courier Assignment срока не имеет: workflow ждёт `CourierAssignedToDelivery`, поиск ведёт Assignment Workflow `delivery` (FR-709).

Отрицательный итог запускает Saga по журналу шагов: `CancelDelivery` → `CancelRestaurantOrder` → `RefundPayment` после Capture или `CancelPaymentAuthorization`, затем `FailOrCancelOrder`. Шаг, закрытый владельцем (отказ ресторана, `PreparationFailed`, `DeliveryFailed`), не компенсируется. Ресторан запрашивается только после записи оплаты в `Order`: при отказе Authorization Restaurant Order не создан и `CancelRestaurantOrder` не нужен; у `RecordPaymentAuthorization` компенсации нет.

Update `CancellationRequested` принимается до Pickup; позже или во время завершения validator отвечает `CancellationRefused`, client-адаптер — `fulfillment.ErrCancellationRefused`.

## Запланированный заказ

Вход с `DeliveryWindow` (из `OrderPlaced`) под patch `scheduled-route` (`workflow.GetVersion` только для такого входа) создаёт `fulfillment.NewScheduled`: `ConfirmSlotReservation` и `PlanSchedule` перед `RequestRestaurantOrder`, stage `scheduled` после Acceptance, моменты плана (`replan_at`, `start_at`, `courier_request_at`) — таймеры главного цикла через `NextDeadline`/`Expire`, шаги `PlanSchedule` (replan, тот же `planned_ready_at`), `QueueRestaurantOrder` (`restaurant_order_id` из результата `RequestRestaurantOrder`), `CreateDelivery`. Компенсация резервации — `ReleaseSlotReservation`; `slot_unavailable` при подтверждении завершает `Order` кодом `slot_unavailable`. Решение — [ADR 5](../../../../docs/adr/0005-order-fulfillment-orchestration.md#запланированный-заказ).

## Activities

Ключ идемпотентности — `order:<order_id>:<step>` (`authorize_payment`, `create_delivery`, …), не зависит от attempt. Запросы к соседям строятся из view use case [Get Fulfillment Order](../../../order/applications/getfulfillmentorder/README.md), который Activity вызывает на каждой попытке: так `market_id` Order ([ADR 41](../../../../../../docs/adr/0041-cell-based-architecture-with-control-plane.md)) попадает в `payments.CreatePayment` и `delivery.CreateDelivery`. Данные `Order` не добавляются во вход (`Input`, `StepInput`) и состояние workflow: в Event History уже начатых workflow таких полей нет, а сохранённый `Order` их имеет, поэтому replay детерминирован без versioning. Отказ платежа — `PaymentDecision`, а не ошибка. `RecordPaymentAuthorization` вызывает use case [Record Payment Authorization](../../../order/applications/recordpaymentauthorization/README.md) с `StepInput.DecidedAt` (время Workflow, как у `CompleteOrder`); повтор после commit — no-op. Retryability задаёт таблица `retryabilityByKind` по виду `errorkind.Classify`: `invalid_input` — `InvalidInput`, `not_found` — `OrderNotFound`, `domain_rule` (запрет FSM, guard, правило домена) — `InvalidTransition`, все non-retryable; остальное повторяется (Start-To-Close 30s, 1s → ×2 → 1m).

Приоритет задач ([ADR 5](../../../../docs/adr/0005-order-fulfillment-orchestration.md#приоритет-и-fairness-task-queue)): `Client.Start` задаёт fairness key `restaurant_id` в `StartWorkflowOptions.Priority` — workflow tasks и Activities наследуют его; `activityContext` ставит priority key из `activityPriority` (`PriorityCustomer` 1, `PriorityFulfillment` 2, `PriorityBookkeeping` 3). `Register` отказывает Activity без уровня. Ключи не входят в `Input` и `StepInput`: это опции задач, replay от них не зависит.

## Worker

Процесс `cmd/orders-worker` (composition root `di.InitializeWorker`, provider set `slices/fulfillment/di.WorkerSet`) регистрирует workflow и Activities через `Register`, а при `PublishStage` вызывает `EnsureStageSearchAttribute`: Keyword-атрибут `OrderFulfillmentStage` должен существовать в namespace, иначе worker создаёт его (`TEMPORAL__REGISTER_SEARCH_ATTRIBUTES=true`) или не стартует. Client-порты соседей ([`applications/neighbours`](../../applications/neighbours/neighbours.go)) реализуют [`slices/fulfillment/infrastructure/neighbours`](../neighbours) — без зависимости от Temporal-адаптера: `Unavailable` (retryable ошибка), `Simulated` (только локально) и `grpcclient` — командные RPC соседей через internal gateway.

## Replay

`replay_test.go` воспроизводит записанные histories из `testdata/`: ASAP-заказ, завершённый кодом до ветки `scheduled-route`, и запланированный заказ в stage `scheduled`. Изменение кода workflow, ломающее любую из них, вводится новым patch. Прочие изменения по-прежнему несовместимы с replay ранее начатых workflow — перед выкаткой в kind их нужно дождаться или прервать.

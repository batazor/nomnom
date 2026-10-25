# Temporal-адаптер fulfillment

Temporal-адаптер процесса Order Fulfillment по [ADR 5](../../../../docs/adr/0005-order-fulfillment-orchestration.md) и [спецификации `order-fulfillment-orchestration`](../../../../../../openspec/specs/order-fulfillment-orchestration/spec.md). SDK — `go.temporal.io/sdk` v1.50.0. Go package — `temporal`; внешние импортёры дают ему alias (`fulfillmenttemporal`), потому что SDK тоже называется `temporal`.

Правила процесса принимает доменная модель [`domains/fulfillment`](../../domains/fulfillment) слайса [`fulfillment`](../../README.md): адаптер переводит Signals и Update в её вызовы, исполняет её решения (`Step`) как Activities и таймеры и публикует её `Stage` как Search Attribute. Время в модель передаётся из `workflow.Now`.

| Файл | Содержание |
| --- | --- |
| `contract.go` | Workflow ID `order:<order_id>`, task queue `orders`, имена Signals/Update/Activities, payloads, коды причин; `Outcome`, `Stage`, `OperatorChoice` и коды причин — alias'ы типов модели |
| `policy.go` | `Policy` — deadlines модели во входе workflow плюс переключатель `PublishStage`; `DefaultPolicy` содержит placeholders, а не продуктовые сроки |
| `workflow.go` | Главный цикл: исполняет `Next` модели как Activity, ждёт `NextDeadline`, вызывает `Expire`, публикует `Stage`, исполняет план `Compensate` |
| `signals.go` | Signals → `Apply`/`Decide` модели и журнал непринятых фактов; Update `CancellationRequested`: validator — `AdmitCancellation`, handler — `AcceptCancellation` |
| `activities.go` | Activities и client-порты `Payments`, `Restaurants`, `Delivery` |
| `client.go` | Реализация порта [`fulfillment`](../../../order/applications/fulfillment/README.md) слайса `order` на Temporal client |
| `searchattribute.go` | Проверка и регистрация Search Attribute `OrderFulfillmentStage` |
| `register.go` | Регистрация workflow и Activities в worker |

Workflow не импортирует домен `Order` и handlers: Signals и Update меняют только состояние модели, Activities вызываются из главного цикла. Тесты адаптера проверяют интеграцию (Signals доходят до модели, Activities вызываются по её решениям с прежними именами и idempotency key, Search Attribute публикуется) и сквозные сценарии завершения, отмены и компенсации; правила проверяют unit-тесты модели.

## Маршрут

```text
AuthorizePayment → RequestRestaurantOrder → [RestaurantOrderAccepted] → CreateDelivery
→ [CourierAssignedToDelivery ∥ RestaurantOrderReady] → [DeliveryPickedUp] → CapturePayment
→ [DeliveryCompleted] → CompleteOrder
```

| Deadline (`Policy`) | По истечении |
| --- | --- |
| `AcceptanceTimeout` | `restaurant_acceptance_timeout` |
| `AssignmentTimeout` | `ReplanDelivery` до `MaxReplans`, затем stage `waiting_operator` |
| `OperatorDecisionTimeout` | `courier_assignment_exhausted` |
| `PreparationTimeout` (+ `PreparationDelayed` до `MaxPreparationExtension`) | `preparation_timeout` |
| `PickupTimeout` | `pickup_timeout` |
| `DeliveryTimeout` | `delivery_timeout` |

Отрицательный итог запускает Saga по журналу шагов: `CancelDelivery` → `CancelRestaurantOrder` → `RefundPayment` после Capture или `CancelPaymentAuthorization`, затем `FailOrCancelOrder`. Шаг, закрытый владельцем (отказ ресторана, `PreparationFailed`, `DeliveryFailed`), не компенсируется.

Update `CancellationRequested` принимается до Pickup; позже или во время завершения validator отвечает `CancellationRefused`, client-адаптер — `fulfillment.ErrCancellationRefused`.

## Activities

Ключ идемпотентности — `order:<order_id>:<step>` (`authorize_payment`, `replan_delivery_2`, …), не зависит от attempt. Запросы к соседям строятся из snapshot `Order`. Отказ платежа — `PaymentDecision`, а не ошибка. `InvalidInput`, `OrderNotFound` и `InvalidTransition` — non-retryable; остальное повторяется (Start-To-Close 30s, 1s → ×2 → 1m).

## Worker

Процесс `cmd/orders-worker` (composition root `di.InitializeWorker`, provider set `slices/fulfillment/di.WorkerSet`) регистрирует workflow и Activities через `Register`, а при `PublishStage` вызывает `EnsureStageSearchAttribute`: Keyword-атрибут `OrderFulfillmentStage` должен существовать в namespace, иначе worker создаёт его (`TEMPORAL__REGISTER_SEARCH_ATTRIBUTES=true`) или не стартует. Client-порты соседей реализуют [`slices/fulfillment/infrastructure/neighbours`](../neighbours): `Unavailable` (retryable ошибка), `Simulated` (только локально) и `grpcclient` — командные RPC соседей через internal gateway.

## Не реализовано

- Kafka consumers, вызывающие `Notifier` и `StartFromOrderPlaced`;
- replay-тесты на записанных histories; изменение кода workflow несовместимо с replay ранее начатых workflow — перед выкаткой в kind их нужно дождаться или прервать.

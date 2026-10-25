# fulfillorder

Temporal-адаптер Order Fulfillment Workflow по [ADR 5](../../../../../docs/adr/0005-order-fulfillment-orchestration.md) и [спецификации `order-fulfillment-orchestration`](../../../../../../../openspec/specs/order-fulfillment-orchestration/spec.md). SDK — `go.temporal.io/sdk` v1.50.0.

| Файл | Содержание |
| --- | --- |
| `contract.go` | Workflow ID `order:<order_id>`, task queue `orders`, имена Signals/Update/Activities, payloads, коды причин |
| `policy.go` | `Policy` — deadlines во входе workflow; `DefaultPolicy` содержит placeholders, а не продуктовые сроки |
| `workflow.go` | Главный цикл: шаги маршрута, deadlines, Workflow Stage, Saga |
| `signals.go` | Signals с дедупликацией по `event_id` и проверкой stage, `OperatorDecision`, Update `CancellationRequested` |
| `activities.go` | Activities и client-порты `Payments`, `Restaurants`, `Delivery` |
| `client.go` | Реализация порта [`fulfillment`](../../../applications/fulfillment/README.md) на Temporal client |
| `register.go` | Регистрация workflow и Activities в worker |

Workflow не импортирует домен и handlers: Signals и Update меняют только состояние, Activities вызываются из главного цикла.

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

Процесс `cmd/orders-worker` (composition root `di.InitializeWorker`) регистрирует workflow и Activities через `Register`, а при `PublishStage` вызывает `EnsureStageSearchAttribute`: Keyword-атрибут `OrderFulfillmentStage` должен существовать в namespace, иначе worker создаёт его (`TEMPORAL__REGISTER_SEARCH_ATTRIBUTES=true`) или не стартует. Client-порты соседей реализуют `slices/order/infrastructure/neighbours`: `Unavailable` (retryable ошибка), `Simulated` (только локально) и `grpcclient` — командные RPC соседей через internal gateway.

## Не реализовано

- Kafka consumers, вызывающие `Notifier` и `StartFromOrderPlaced`;
- replay-тесты на записанных histories.

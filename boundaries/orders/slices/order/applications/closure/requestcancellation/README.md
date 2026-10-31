# Request Order Cancellation

## Сценарий

- **Тип:** Command.
- **Actor:** Клиент или другой авторизованный actor.
- **Намерение:** Запросить прекращение незавершённого заказа.
- **Результат:** Права и допустимость проверены; запрос передан существующему fulfillment workflow.

## Границы

- **Владелец:** `orders/slices/order`.
- **Домен:** `domains/order`.
- **Взаимодействие:** Authoritative authorization выполняется в `orders`; компенсации координирует workflow, а не этот use case.

## Последовательность

```mermaid
sequenceDiagram
    actor Actor as Авторизованный actor
    participant App as Request Cancellation
    participant Auth as Authorization
    participant Order as Order
    participant Workflow as Fulfillment workflow
    Actor->>App: запросить отмену
    App->>Auth: проверить право
    Auth-->>App: разрешено
    App->>Order: проверить допустимость запроса
    App->>Workflow: передать запрос отмены
    Workflow-->>App: запрос принят
    App-->>Actor: результат
```

## Реализация

Handler: [`requestcancellation.go`](requestcancellation.go), сценарии: [`acceptance_test.go`](acceptance_test.go), остальные случаи: [`requestordercancellation_test.go`](requestordercancellation_test.go).

- **Вход:** `Input{Subject, OrderID, RequestID, Reason}`.
- **Порядок:** authorization `cancel` на `order:<id>` (fail closed) → `Orders.Get` → `CancellationPolicy.Window(placedAt)` → `Order.CheckCancellation(now, window)` → `fulfillment.CancellationRequester.RequestCancellation` с исходным subject. UoW не используется: запрос локально не сохраняется.
- **Результат:** `requested` или `already_cancelled`; `completed` — `InvalidTransition`, окно — `CancellationNotAllowed{Reasons}`, отказ workflow — ошибка порта.
- **Допущение:** окно отмены — продуктовый вопрос и остаётся за портом `CancellationPolicy`.
- **Требования:** FR-111, FR-112; AC-115…AC-119 в OpenSpec change `implement-orders-application-layer`.

## Acceptance

Executable-сценарии: [`requestcancellation.feature`](acceptance/requestcancellation.feature) исполняются `godog` под `go test` против PostgreSQL (testcontainers); сценарии `@pending` ждут переноса AC-916 в `openspec/specs`.

Общий каталог и место сценария в fulfillment process: [Orders use cases](../../../../../README.md#use-cases).

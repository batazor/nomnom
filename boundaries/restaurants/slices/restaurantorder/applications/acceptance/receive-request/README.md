# Receive Order Request

## Сценарий

- **Тип:** Command.
- **Actor:** Fulfillment process `orders`.
- **Намерение:** Передать ресторану состав заказа для решения и приготовления.
- **Результат:** Идемпотентно создан локальный `Kitchen Order` в состоянии ожидания решения, в Market ресторана (ADR 41). Запланированный заказ приходит с блоком `schedule` (`window_start`, `window_end`, `planned_ready_at`), который хранится как данные, а не как State; неполный блок, пустое окно или `planned_ready_at` не раньше `window_start` — `INVALID_ARGUMENT` (FR-683).

## Границы

- **Владелец:** `restaurants/slices/restaurantorder`.
- **Домен:** `domains/restaurantorder`.
- **Взаимодействие:** Вызывается только Temporal Activity `RequestRestaurantOrder` через RPC `ReceiveOrderRequest` (FR-428) — после одобренной оплаты, поэтому кухня не видит неоплаченных заказов ([ADR 5 orders](../../../../../../orders/docs/adr/0005-order-fulfillment-orchestration.md#saga)); `OrderPlaced` Restaurant Order не создаёт. Повтор того же состава (с тем же `schedule`) по `order_id` возвращает `created = false`; конфликтующий повтор — `ALREADY_EXISTS`. Market Restaurant Order берётся из ресторана (порт `RestaurantMarkets`); RPC Market не передаёт.

## Последовательность

```mermaid
sequenceDiagram
    participant Workflow as Orders workflow
    participant API as Restaurants API
    participant App as Receive Order Request
    participant Kitchen as Kitchen Order
    participant Store as Unit of Work
    Workflow->>API: передать состав заказа
    API->>App: идемпотентная команда
    App->>Kitchen: создать ожидающий заказ
    App->>Store: сохранить Kitchen Order
    Store-->>App: commit
    App-->>Workflow: локальный идентификатор / результат
```

## Acceptance

BDD-сценарии размещаются в `acceptance/receive-request.feature` после утверждения acceptance criteria в SDD.

Общий каталог: [Restaurants use cases](../../../../../README.md#use-cases).


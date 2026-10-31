# List Customer Orders

## Сценарий

- **Тип:** Query.
- **Actor:** Клиент или внутренний авторизованный actor.
- **Намерение:** Получить историю заказов клиента.
- **Результат:** Возвращён доступный actor список заказов без изменения состояния.

## Границы

- **Владелец:** `orders/slices/order`.
- **Домен:** `domains/order`.
- **Взаимодействие:** Читает read model `orders` и проверяет принадлежность и право просмотра.

## Реализация

Handler: [`listforcustomer.go`](listforcustomer.go), сценарии: [`acceptance_test.go`](acceptance_test.go), остальные случаи: [`listcustomerorders_test.go`](listcustomerorders_test.go).

- **Вход:** `Query{Subject, CustomerID, MarketID, Limit, Cursor}`; `MarketID` обязателен ([ADR 41](../../../../../../docs/adr/0041-cell-based-architecture-with-control-plane.md)): пустой или не slug `^[a-z][a-z0-9-]{1,31}$` — `INVALID_ARGUMENT`; `Limit` по умолчанию 20, максимум 100.
- **Порядок:** собственная история Customer фильтруется по `customer_id` без authorization graph; другой subject проходит `view_orders` на `customer:<id>` (fail closed) → `Reader.ListCustomerOrders` — страница проекции `OrderDetails` только Market `MarketID` (фильтр в SQL, индекс `order_details_customer_market_history (customer_id, market_id, placed_at DESC, order_id DESC)`) → Cancellation Window каждого заказа.
- **Результат:** `Page{Orders, NextCursor}` от новых к старым по `placedAt`, затем по `order_id`; каждый `Entry` — полная карточка заказа, та же, что у Get Order (читателю истории не нужен Get Order на каждый заказ), и `Cursor` — продолжение истории после него (cursor ребра GraphQL `customerOrders` в BFF), у последнего на странице он равен `NextCursor`.
- **Требования:** FR-116; AC-128, AC-129 в OpenSpec change `implement-orders-application-layer`.

## Acceptance

Executable-сценарии: [`listforcustomer.feature`](acceptance/listforcustomer.feature) исполняются `godog` под `go test` против PostgreSQL (testcontainers); сценарий `@pending` о Market (ADR 41) ждёт своего AC.

Общий каталог и место сценария в fulfillment process: [Orders use cases](../../../../README.md#use-cases).

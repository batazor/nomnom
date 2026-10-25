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

Handler: [`listcustomerorders.go`](listcustomerorders.go), unit-тесты: [`listcustomerorders_test.go`](listcustomerorders_test.go).

- **Вход:** `Query{Subject, CustomerID, Limit, Cursor}`; `Limit` по умолчанию 20, максимум 100.
- **Порядок:** собственная история Customer фильтруется по `customer_id` без authorization graph; другой subject проходит `view_orders` на `customer:<id>` (fail closed) → `Reader.ListCustomerOrders`.
- **Результат:** `Page{Orders, NextCursor}` от новых к старым по `placedAt`, затем по `order_id`.
- **Требования:** FR-116; AC-128, AC-129 в OpenSpec change `implement-orders-application-layer`.

## Acceptance

BDD-сценарии размещаются в `acceptance/listcustomerorders.feature` после утверждения acceptance criteria в SDD.

Общий каталог и место сценария в fulfillment process: [Orders use cases](../../../../README.md#use-cases).

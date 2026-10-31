# Get Order

## Сценарий

- **Тип:** Query.
- **Actor:** Клиент или внутренний авторизованный actor.
- **Намерение:** Получить текущее представление одного заказа.
- **Результат:** Возвращено доступное actor представление заказа без изменения состояния.

## Границы

- **Владелец:** `orders/slices/order`.
- **Домен:** `domains/order`.
- **Взаимодействие:** Читает read model `orders` и проверяет право просмотра.

## Реализация

Handler: [`getorder.go`](getorder.go), сценарии: [`acceptance_test.go`](acceptance_test.go), остальные случаи: [`getorder_test.go`](getorder_test.go).

- **Вход:** `Query{Subject, OrderID}`.
- **Порядок:** authorization `view` на `order:<id>` до чтения (fail closed, не раскрывает существование) → `Reader.OrderDetails` — строка проекции `OrderDetails` ([ADR 4](../../../../docs/adr/0004-использование-cqrs-в-orders.md#проекция-orderdetails)) без восстановления агрегата → Cancellation Window политики на момент чтения. UoW не используется.
- **Результат:** `orderdetails.View` — read model и Application DTO: состав с названиями позиций на момент Place Order, total, Order State, `placedAt`, отражённые факты, сроки, причина отрицательного итога и Cancellation Window.
- **Требования:** FR-115; AC-126, AC-127 в OpenSpec change `implement-orders-application-layer`.

## Acceptance

Executable-сценарии: [`getorder.feature`](acceptance/getorder.feature) исполняются `godog` под `go test` против PostgreSQL (testcontainers).

Общий каталог и место сценария в fulfillment process: [Orders use cases](../../../../README.md#use-cases).

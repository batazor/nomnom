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

Handler: [`getorder.go`](getorder.go), unit-тесты: [`getorder_test.go`](getorder_test.go).

- **Вход:** `Query{Subject, OrderID}`.
- **Порядок:** authorization `view` на `order:<id>` до чтения (fail closed, не раскрывает существование) → `Reader.OrderView`. UoW не используется.
- **Результат:** `OrderView` — read model и Application DTO: состав, total, Order State, `placedAt`, отражённые факты и причина отрицательного итога.
- **Требования:** FR-115; AC-126, AC-127 в OpenSpec change `implement-orders-application-layer`.

## Acceptance

BDD-сценарии размещаются в `acceptance/getorder.feature` после утверждения acceptance criteria в SDD.

Общий каталог и место сценария в fulfillment process: [Orders use cases](../../../../README.md#use-cases).

# Get Fulfillment Order

## Сценарий

- **Тип:** Query.
- **Actor:** Temporal fulfillment workflow.
- **Намерение:** Получить данные `Order`, из которых Activity строит запрос к `payments`, `restaurants` или `delivery`.
- **Результат:** View без доменных типов: участники, Market, сумма, позиции, приоритет и Delivery Window. Состояние `Order` не меняется.

## Границы

- **Владелец:** `orders/slices/order`.
- **Домен:** `domains/order`.
- **Взаимодействие:** Activities вызывают сценарий на каждой попытке ([ADR 5](../../../../../docs/adr/0005-order-fulfillment-orchestration.md)). Читается сам `Order`, а не проекция `OrderDetails`, поэтому отставания проекции нет. Вызывает `service` identity worker'а, permission check не выполняется.

# Spec Delta

## ADDED Requirements

### Requirement: Place Order читает каталог из restaurants
**ID:** FR-497

Production-адаптер каталога Place Order SHALL строить snapshot из `restaurants.GetRestaurant` и `GetMenu` до транзакции: цены и доступность запрошенных позиций, минимальная и максимальная сумма — из конфигурации. Закрытый ресторан MUST отклонять оформление до транзакции, недоступность `restaurants` MUST давать `UNAVAILABLE`.

#### Scenario: Закрытый ресторан
**ID:** AC-566

- **WHEN** покупатель оформляет заказ в ресторане с `is_open = false`
- **THEN** `PlaceOrder` отвечает `FAILED_PRECONDITION` с `ErrorInfo.reason = RESTAURANT_NOT_ACCEPTING_ORDERS`, и `Order` не создаётся

### Requirement: Отказ Place Order называет причины
**ID:** FR-498

Отказ политики `OrderItemsOrderable` SHALL возвращаться `FAILED_PRECONDITION` с `ErrorInfo.reason = ORDER_NOT_ORDERABLE` и metadata `codes` и `menu_item_ids`.

#### Scenario: Позиция недоступна
**ID:** AC-567

- **WHEN** заказ содержит недоступную позицию `menu-gyoza`
- **THEN** metadata ответа содержит `codes = ORDER_ITEM_UNAVAILABLE` и `menu_item_ids = menu-gyoza`

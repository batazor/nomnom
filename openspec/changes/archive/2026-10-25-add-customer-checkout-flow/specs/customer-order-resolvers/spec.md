# Spec Delta

## ADDED Requirements

### Requirement: Каталог ресторанов в BFF
**ID:** FR-499

Resolvers `restaurants(first, after)` и `restaurant(id)` SHALL читать публичные данные `restaurants` без access token; `restaurant(id)` SHALL возвращать меню с ценами и доступностью, `null` для отсутствующего ресторана и `menu = null` для ресторана без меню.

#### Scenario: Ресторан с меню
**ID:** AC-568

- **WHEN** клиент без access token запрашивает `restaurant(id)` ресторана с меню
- **THEN** ответ содержит ресторан и его позиции с ценами и доступностью

### Requirement: placeOrder оформляет заказ от имени покупателя
**ID:** FR-500

Resolver `placeOrder` SHALL требовать access token, вызывать `orders.PlaceOrder` от имени покупателя с `idempotencyKey`, возвращать представление оформленного заказа и переводить отказы `ORDER_NOT_ORDERABLE` и `RESTAURANT_NOT_ACCEPTING_ORDERS` в `problems` без GraphQL errors.

#### Scenario: Недоступная позиция
**ID:** AC-569

- **WHEN** `orders` отвечает `ORDER_NOT_ORDERABLE` с `codes = ORDER_ITEM_UNAVAILABLE` и `menu_item_ids = menu-gyoza`
- **THEN** payload содержит `order = null` и problem `ORDER_ITEM_UNAVAILABLE` с `menuItemIds = [menu-gyoza]`

#### Scenario: Успешное оформление
**ID:** AC-570

- **WHEN** покупатель с валидным access token оформляет заказ из доступных позиций
- **THEN** payload содержит заказ со статусом `RECEIVED` и без problems

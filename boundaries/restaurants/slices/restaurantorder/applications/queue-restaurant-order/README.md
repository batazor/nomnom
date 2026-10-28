# Queue Restaurant Order

## Сценарий

- **Тип:** Command.
- **Actor:** `service` identity процесса `orders` (workflow запланированного заказа в `start_at`).
- **Намерение:** Поставить принятый `Restaurant Order` в Kitchen Queue.
- **Результат:** Заказ в `QUEUED` с `queued_at`; `expected_ready_at` не меняется, приготовление не начато (FR-224).

## Границы

- **Владелец:** `restaurants/slices/restaurantorder`.
- **Домен:** `domains/restaurantorder`.
- **Взаимодействие:** Заказ без окна ставится в очередь внутри Accept Order в одной Unit of Work. Запланированный заказ остаётся в `ACCEPTED`, и его ставит в очередь gRPC `QueueRestaurantOrder(restaurant_order_id)` — только `service` identity `orders` (платформенная проверка `coordinate_fulfillment` до чтения заказа); сотрудник с `operate_kitchen` получает `PERMISSION_DENIED` (AC-924). Факт `RestaurantOrderQueued` локальный и не публикуется; запись уведомляет kitchen board (ADR 36). Повтор для `QUEUED` — успешный no-op без изменения версии.

## Acceptance

BDD-сценарии размещаются в `acceptance/queue-restaurant-order.feature` после утверждения acceptance criteria в SDD.

Общий каталог: [Restaurants use cases](../../../../README.md#use-cases).

# Queue Restaurant Order

## Сценарий

- **Тип:** Command.
- **Actor:** Система `restaurants`.
- **Намерение:** Поставить принятый `Restaurant Order` в Kitchen Queue.
- **Результат:** Заказ в `QUEUED` с `queued_at`; приготовление не начато.

## Границы

- **Владелец:** `restaurants/slices/restaurantorder`.
- **Домен:** `domains/restaurantorder`.
- **Взаимодействие:** В MVP выполняется внутри Accept Order в одной Unit of Work; отдельный handler покрывает отложенную постановку заказа, оставшегося в `ACCEPTED`. Факт `RestaurantOrderQueued` локальный и не публикуется; повтор для `QUEUED` — успешный no-op.

## Acceptance

BDD-сценарии размещаются в `acceptance/queue-restaurant-order.feature` после утверждения acceptance criteria в SDD.

Общий каталог: [Restaurants use cases](../../../../README.md#use-cases).

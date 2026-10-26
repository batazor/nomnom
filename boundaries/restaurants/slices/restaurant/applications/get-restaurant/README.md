# Get Restaurant

## Сценарий

- **Тип:** Query.
- **Actor:** Клиент или внутренний actor.
- **Намерение:** Получить публичное представление ресторана.
- **Результат:** Возвращены доступные публичные данные с Market ресторана (`market_id`) без изменения состояния.

## Границы

- **Владелец:** `restaurants/slices/restaurant`.
- **Домен:** `domains/restaurant`.
- **Взаимодействие:** Читает query model `restaurants`; опубликован RPC `GetRestaurant`.

## Acceptance

BDD-сценарии размещаются в `acceptance/get-restaurant.feature` после утверждения acceptance criteria в SDD.

Общий каталог: [Restaurants use cases](../../../../README.md#use-cases).


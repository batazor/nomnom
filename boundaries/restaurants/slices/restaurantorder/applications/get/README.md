# Get Restaurant Order

## Сценарий

- **Тип:** Query.
- **Actor:** Авторизованный сотрудник ресторана или внутренний actor.
- **Намерение:** Получить состав, State и timeline заказа.
- **Результат:** Представление одного `Restaurant Order` без изменения состояния.

## Границы

- **Владелец:** `restaurants/slices/restaurantorder`.
- **Домен:** `domains/restaurantorder`.
- **Взаимодействие:** Читает read model `restaurants`; заказ другого ресторана не раскрывается и возвращается как not found.

## Acceptance

BDD-сценарии размещаются в `acceptance/get.feature` после утверждения acceptance criteria в SDD.

Общий каталог: [Restaurants use cases](../../../../README.md#use-cases).

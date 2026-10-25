# Mark Assembled

## Сценарий

- **Тип:** Command.
- **Actor:** Авторизованный сотрудник кухни.
- **Намерение:** Подтвердить, что все required items приготовлены и собраны в один заказ.
- **Результат:** `Restaurant Order` в `ASSEMBLED`.

## Границы

- **Владелец:** `restaurants/slices/kitchenorder`.
- **Домен:** `domains/restaurantorder`.
- **Взаимодействие:** Без item-level model команда сама является утверждением о готовности позиций. Факт `RestaurantOrderAssembled` локальный; Outbox и Unit of Work не используются.

## Acceptance

BDD-сценарии размещаются в `acceptance/mark-assembled.feature` после утверждения acceptance criteria в SDD.

Общий каталог: [Restaurants use cases](../../../../README.md#use-cases).

# List Active Preparations

## Сценарий

- **Тип:** Query.
- **Actor:** Авторизованный сотрудник ресторана.
- **Намерение:** Увидеть заказы в работе кухни.
- **Результат:** Заказы в `PREPARING` и `ASSEMBLED` с `preparation_started_at` и текущим `expected_ready_at`.

## Границы

- **Владелец:** `restaurants/slices/restaurantorder`.
- **Домен:** `domains/restaurantorder`.
- **Взаимодействие:** Читает read model kitchen board.

## Acceptance

BDD-сценарии размещаются в `acceptance/list-active-preparations.feature` после утверждения acceptance criteria в SDD.

Общий каталог: [Restaurants use cases](../../../../../README.md#use-cases).

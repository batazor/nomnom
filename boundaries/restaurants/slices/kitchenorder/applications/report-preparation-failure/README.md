# Report Preparation Failure

## Сценарий

- **Тип:** Command.
- **Actor:** Авторизованный сотрудник кухни.
- **Намерение:** Сообщить, что начатое приготовление невозможно завершить.
- **Результат:** `Restaurant Order` в `FAILED` с обязательной причиной.

## Границы

- **Владелец:** `restaurants/slices/kitchenorder`.
- **Домен:** `domains/restaurantorder`.
- **Взаимодействие:** Допустим в `PREPARING`, `ASSEMBLED`. Записывает `RestaurantOrderFailed` (кандидат Integration Event `PreparationFailed`) через Transactional Outbox.

## Acceptance

BDD-сценарии размещаются в `acceptance/report-preparation-failure.feature` после утверждения acceptance criteria в SDD.

Общий каталог: [Restaurants use cases](../../../../README.md#use-cases).

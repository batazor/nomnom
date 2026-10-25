# Report Preparation Delay

## Сценарий

- **Тип:** Command.
- **Actor:** Авторизованный сотрудник кухни.
- **Намерение:** Сообщить, что заказ будет готов позже.
- **Результат:** `expected_ready_at` перенесён на более позднее время, State не меняется.

## Границы

- **Владелец:** `restaurants/slices/restaurantorder`.
- **Домен:** `domains/restaurantorder`.
- **Взаимодействие:** Допустим в `QUEUED`, `PREPARING`, `ASSEMBLED`; новое время позже текущего `expected_ready_at` и текущего момента. Записывает `PreparationDelayed` через Transactional Outbox.

## Acceptance

BDD-сценарии размещаются в `acceptance/report-preparation-delay.feature` после утверждения acceptance criteria в SDD.

Общий каталог: [Restaurants use cases](../../../../README.md#use-cases).

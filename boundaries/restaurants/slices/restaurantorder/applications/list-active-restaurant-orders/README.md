# List Active Restaurant Orders

## Сценарий

- **Тип:** Query.
- **Actor:** Авторизованный сотрудник ресторана (`restaurant#operate_kitchen`).
- **Намерение:** Увидеть всю текущую работу кухни одним списком — от поступления заказа до передачи курьеру.
- **Результат:** Заказы в `RECEIVED`, `QUEUED`, `PREPARING`, `ASSEMBLED` и `READY` по возрастанию `received_at` (затем по id) с состоянием, позициями, `received_at`, `expected_ready_at` и версией.

## Границы

- **Владелец:** `restaurants/slices/restaurantorder`.
- **Домен:** `domains/restaurantorder`.
- **Взаимодействие:** Читает read model kitchen board. Рекомендованный порядок заказов в `QUEUED` — по-прежнему [Get Kitchen Queue](../get-kitchen-queue/README.md); этот список его не повторяет. Клиент — kitchen-экран BFF (`kitchen(restaurantId)`).

## Acceptance

BDD-сценарии размещаются в `acceptance/list-active-restaurant-orders.feature` после утверждения acceptance criteria в SDD.

Общий каталог: [Restaurants use cases](../../../../README.md#use-cases).

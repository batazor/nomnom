# Get Kitchen Queue

## Сценарий

- **Тип:** Query.
- **Actor:** Авторизованный сотрудник ресторана.
- **Намерение:** Увидеть заказы, ожидающие Preparation.
- **Результат:** Заказы в `QUEUED` в рекомендованном порядке с `expected_ready_at`.

## Границы

- **Владелец:** `restaurants/slices/restaurantorder`.
- **Домен:** `domains/restaurantorder`.
- **Взаимодействие:** Читает projection Kitchen Queue, которая не является aggregate. Порядок задаёт port политики очереди: алгоритм — открытый продуктовый вопрос.

## Acceptance

BDD-сценарии размещаются в `acceptance/get-queue.feature` после утверждения acceptance criteria в SDD.

Общий каталог: [Restaurants use cases](../../../../../README.md#use-cases).

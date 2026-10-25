# List Restaurants

## Сценарий

- **Тип:** Query.
- **Actor:** Клиент.
- **Намерение:** Найти доступные для выбора рестораны.
- **Результат:** Возвращён список доступных ресторанов без изменения состояния.

## Границы

- **Владелец:** `restaurants/slices/restaurant`.
- **Домен:** `domains/restaurant`.
- **Взаимодействие:** Читает query model; критерии доступности уточняются в SDD.

## Acceptance

BDD-сценарии размещаются в `acceptance/list-restaurants.feature` после утверждения acceptance criteria в SDD.

Общий каталог: [Restaurants use cases](../../../../README.md#use-cases).


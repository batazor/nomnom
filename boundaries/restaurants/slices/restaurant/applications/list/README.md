# List Restaurants

## Сценарий

- **Тип:** Query.
- **Actor:** Клиент.
- **Намерение:** Найти доступные для выбора рестораны своего Market.
- **Результат:** Возвращён список доступных ресторанов обязательного Market (`market_id`, ADR 41) без изменения состояния; рестораны других Market не возвращаются.

## Границы

- **Владелец:** `restaurants/slices/restaurant`.
- **Домен:** `domains/restaurant`.
- **Взаимодействие:** Читает query model по `marketId` (индексы `restaurants_market_open_by_id`, `restaurants_market_by_id`) через кеш Valkey со страницами и поколением на каждый Market; пустой или не slug `market_id` — `INVALID_ARGUMENT`; критерии доступности уточняются в SDD.

## Acceptance

BDD-сценарии размещаются в `acceptance/list.feature` после утверждения acceptance criteria в SDD.

Общий каталог: [Restaurants use cases](../../../../README.md#use-cases).


# Record Wait Time

## Сценарий

- **Тип:** Command, вызывается consumer'ом собственных Integration Events.
- **Actor:** `restaurants-consumer` по фактам `restaurants.v1.restaurant-order-events`.
- **Намерение:** Держать статистику ожидания ресторана актуальной.
- **Результат:** Wait Time Statistics ресторана обновлена; при смене уровня ожидания записан `RestaurantWaitLevelChanged`.

## Правила

- `RestaurantOrderAccepted` добавляет незавершённый заказ, `RestaurantOrderReady` переносит его длительность в выборку, `RestaurantOrderCancelled` и `PreparationFailed` убирают его без длительности (FR-644).
- Повторный факт ничего не меняет. Ресторана нет — `NotFoundError`, событие уходит в DLQ.
- Базовое время, текущее ожидание и уровень с гистерезисом считает `WaitTimePolicy` (FR-645, FR-657). Линия поваров в расчёте не участвует.
- Статистика — read model в документе ресторана: запись не меняет `version` агрегата `Restaurant`.
- Смена уровня и `RestaurantWaitLevelChanged` фиксируются одной Unit of Work (FR-646); версия события — счётчик смен уровня.

## Границы

- **Владелец:** `restaurants/slices/restaurant`.
- **Домен:** `domains/restaurant/wait-time.ts`.
- **Взаимодействие:** Transactional Outbox, topic `restaurants.v1.restaurant-events`.

Общий каталог: [Restaurants use cases](../../../../README.md#use-cases).

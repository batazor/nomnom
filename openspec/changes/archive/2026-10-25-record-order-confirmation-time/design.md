# Design

## Decisions

### 1. Факт, а не поле Order

Момент подтверждения — такой же бизнес-факт соседа, как `preparation_started` или `courier_assigned`: его сообщает `restaurants`, он хранится в `order_facts` и публикуется в `fulfillment_facts`. Отдельное поле `confirmed_at` потребовало бы миграции и нового поля proto без выигрыша.

### 2. Запись вместе с переходом

`Order.Confirm(occurredAt)` выполняет переход `confirm` и добавляет `RecordedFact{restaurant_accepted, occurredAt}`. Повтор подтверждения (`AlreadyInState`) факт не дублирует. Пустой `occurredAt` — `GuardFailed{occurred_at_required}`, как у остальных фактов.

### 3. BFF

`milestoneOfFact["restaurant_accepted"] = CONFIRMED`; порядок milestones по времени не меняется.

## Risks / Trade-offs

- [Старые заказы без `CONFIRMED` в истории] → честное отсутствие данных лучше выдуманного времени; статус и прогресс экрана от этого не зависят.

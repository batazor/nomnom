# List Scheduled Orders

## Сценарий

- **Тип:** Query.
- **Actor:** Авторизованный сотрудник ресторана (`restaurant#operate_kitchen`).
- **Намерение:** Увидеть принятые запланированные заказы, которые ждут своего старта (колонка «Запланированные»).
- **Результат:** Заказы в `ACCEPTED` с окном доставки по возрастанию `planned_ready_at` (затем по id) с `restaurant_order_id`, `order_id`, позициями, окном, `planned_ready_at`, приоритетом и версией (FR-684).

## Границы

- **Владелец:** `restaurants/slices/restaurantorder`.
- **Домен:** `domains/restaurantorder`.
- **Взаимодействие:** Читает read model kitchen board. Запланированный заказ не попадает в Get Kitchen Queue, пока `orders` не поставит его в очередь через Queue Restaurant Order; действий у кухни над ним нет.

## Acceptance

[FR-684, AC-925](../../../../../../../openspec/changes/add-scheduled-orders/specs/kitchen-board/spec.md).

Общий каталог: [Restaurants use cases](../../../../../README.md#use-cases).

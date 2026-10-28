# Reserve Scheduled Slot

## Сценарий

- **Тип:** Command.
- **Actor:** `service` identity процесса `orders` (Place Order запланированного заказа).
- **Намерение:** Удержать место заказа в окне доставки ресторана.
- **Результат:** Slot Reservation `held` с `expiresAt = now + SCHEDULED_SLOT__HOLD_TTL`; ответ — окно `[window_start, window_end)` (FR-674). Повтор с тем же `order_id` возвращает ту же активную резервацию.
- **Отказы:**
  - окно не на сетке, раньше `now + MIN_LEAD`, не раньше горизонта или вне расписания — `INVALID_ARGUMENT`;
  - ресторан без настроек или не принимающий заказы сейчас — `FAILED_PRECONDITION` `SCHEDULED_ORDERS_UNAVAILABLE`;
  - окно заполнено — бизнес-ответ `slot_unavailable` с не более чем `SCHEDULED_SLOT__ALTERNATIVES` ближайшими доступными окнами (FR-695).

## Границы

- **Владелец:** `restaurants/slices/scheduleslot`.
- **Домен:** `domains/scheduleslot` (`ScheduleSlot`, `DeliveryWindows`).
- **Взаимодействие:** Попытка — одна Unit of Work; конкурентная запись окна даёт конфликт `version`, use case повторяет попытку, поэтому ёмкость не превышается. Просроченные `held` окна вычищаются этой записью. Бизнес-факт не публикуется.

Общий каталог: [Restaurants use cases](../../../../README.md#use-cases).

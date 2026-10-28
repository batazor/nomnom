# Confirm Scheduled Slot

## Сценарий

- **Тип:** Command.
- **Actor:** `service` identity процесса `orders` (workflow после Authorization).
- **Намерение:** Закрепить место заказа в окне после оплаты.
- **Результат:** Резервация `held` → `confirmed` и больше не истекает; повтор — no-op (FR-675). Если hold истёк (вычищен или ещё нет), подтверждение заново проверяет текущую ёмкость окна: есть место — `confirmed`, нет — бизнес-ответ `slot_unavailable`. Заказ, которого не знает ни одно окно, — `NOT_FOUND`.

## Границы

- **Владелец:** `restaurants/slices/scheduleslot`.
- **Домен:** `domains/scheduleslot` (`ScheduleSlot`).
- **Взаимодействие:** Окно находится по `order_id`; конкурентная запись повторяется, как в Reserve Scheduled Slot. Бизнес-факт не публикуется.

Общий каталог: [Restaurants use cases](../../../../README.md#use-cases).

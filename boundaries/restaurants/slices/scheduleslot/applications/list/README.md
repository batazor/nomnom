# List Scheduled Slots

## Сценарий

- **Тип:** Query.
- **Actor:** Любой клиент (публичные данные, как Get Restaurant).
- **Намерение:** Узнать окна доставки ресторана, в которые можно оформить запланированный заказ.
- **Результат:** Окна шириной `spec.scheduledSlot.width`, выровненные по UTC, от `now + spec.scheduledSlot.minLead` до `now + spec.scheduledSlot.maxHorizon`, только внутри интервалов расписания между открытием + `openingLead` и закрытием + `closingGrace`, с признаком `available` (FR-673, FR-703). Окно недоступно, если активных резерваций не меньше ёмкости. Ресторан без настроек запланированных заказов или не принимающий заказы сейчас получает пустой список.

## Границы

- **Владелец:** `restaurants/slices/scheduleslot`.
- **Домен:** `domains/scheduleslot` (`DeliveryWindows`, `ScheduleSlot`); расписание и настройки — `Restaurant` слайса `restaurant`.
- **Взаимодействие:** Одно чтение ресторана и сохранённых окон диапазона; окно без документа свободно. Состояние не меняется.

Общий каталог: [Restaurants use cases](../../../../README.md#use-cases).

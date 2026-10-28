# Proposal

## Why

Принимает ли ресторан заказы, сейчас решает только ручной флаг Open/Close. Specification `RestaurantAcceptingOrders` уже проверяет `opensAt`/`closesAt`, но адаптер `UnconstrainedAcceptanceConditions` подставляет бесконечные границы: часов работы в модели нет. Без них нельзя ограничить запланированные заказы временем закрытия (change `add-scheduled-orders`), а владельцу приходится закрывать ресторан руками каждый вечер.

## What Changes

- **Часовой пояс ресторана.** `RegisterRestaurant` требует IANA `time_zone`; ресторан хранит его как неизменяемый атрибут. Контексты Cell не вызывают control plane (ADR 41), поэтому пояс хранится в ресторане, а не читается из Market. Существующим ресторанам migration задаёт `Europe/Berlin`: все они в Market `berlin`.
- **Недельное расписание.** `SetOpeningHours` (`restaurant#manage`) задаёт по дням недели интервалы «открыт с — до» в местном времени; интервал может переходить через полночь. Интервалы недели не пересекаются.
- **Приём заказов** = ручной флаг открыт **и** текущий момент внутри интервала расписания. Флаг остаётся ручной паузой. Ресторан без расписания заказы не принимает: это пустое состояние, а не «круглосуточно».
- **`RestaurantAcceptingOrders`** получает реальные `opensAt`/`closesAt` текущего интервала; заглушка для ресторана удаляется (окна позиций меню остаются как есть).
- **Чтения.** Get Restaurant отдаёт пояс, расписание, `accepting_orders` и `next_opening_at`, вычисленные на момент чтения; List Restaurants по умолчанию отдаёт принимающие сейчас рестораны.
- **UI.** Экран управления рестораном редактирует расписание; каталог покупателя показывает «Откроется в 10:00» у ресторана, который сейчас не принимает заказы.

Не входит: исключения расписания (праздники, `ScheduleException`), окна доступности позиций меню, автоматическое уведомление о закрытии.

## Capabilities

### New Capabilities

- `restaurant-opening-hours`: расписание, перевод в моменты с учётом перевода часов, приём заказов по расписанию.

### Modified Capabilities

- `restaurant-management`: пояс в Register Restaurant, расписание и приём заказов в Get Restaurant и List Restaurants.
- `customer-graphql-contract`: `Restaurant.openingHours`, `nextOpeningAt`, mutation `setOpeningHours`.
- `customer-web-application`: редактор расписания владельца, «Откроется в …» в каталоге.

## Impact

- **`boundaries/restaurants`:** слайс `restaurant` (`timeZone`, `openingHours`, Set Opening Hours, вычисление текущего интервала и следующего открытия), migration документов, gRPC `SetOpeningHours`, поля в `restaurants.proto`; адаптер acceptance conditions вместо `UnconstrainedAcceptanceConditions` для ресторана; кеш каталога хранит расписание и вычисляет приём при чтении.
- **`boundaries/orders`:** нет изменений кода: catalog snapshot уже отклоняет непринимающий ресторан.
- **`boundaries/bff`, `boundaries/ui`:** схема, resolvers, экран управления, каталог, i18n, моки.
- **`ops`:** seed расписания демо-ресторанов (иначе после migration они перестанут принимать заказы), kind e2e.
- **Документация:** `docs/kitchen.md`, `docs/specification-pattern.md`, `docs/ubiquitous-language.md` (Opening Hours, Restaurant Time Zone), README `restaurants`, ADR 0002 `restaurants` (поля документа ресторана).

Предварительный для `add-scheduled-orders`.

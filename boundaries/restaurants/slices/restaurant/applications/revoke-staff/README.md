# Revoke Staff

## Сценарий

- **Тип:** Command.
- **Actor:** Пользователь с `restaurant#manage` (владелец ресторана).
- **Намерение:** Отозвать у пользователя доступ персонала.
- **Результат:** User id удалён из `Restaurant.staff`; связь `restaurant:<id>#staff@user:<userId>` удалена в SpiceDB (`DELETE`).

## Правила

- Без `restaurant#manage` — `PermissionDeniedError`, без записи и сохранения.
- Отзыв пользователя, который не входит в staff, — no-op (`changed: false`) без записи в SpiceDB.
- `DELETE` выполняется до сохранения ресторана (FR-443): при неуспешном сохранении доступ временно пропадает и восстанавливается reconciliation после двух проходов с интервалом не меньше grace, устаревшего доступа не остаётся. Проход, прочитавший ресторан до сохранения revoke, связь не возвращает (FR-445, AC-700). Ошибка SpiceDB отменяет команду.
- Владелец сохраняет `restaurant#owner` независимо от staff.

## Границы

- **Владелец:** `restaurants/slices/restaurant`.
- **Домен:** `domains/restaurant`.
- **Порты:** `RestaurantAccessControl.canManageRestaurant`, `StaffRelationshipWriter.deleteStaff`, `RestaurantRepository`.
- **Transport:** RPC пока нет.

## Acceptance

AC-476 ([restaurant-delivery-relationships](../../../../../../openspec/changes/add-restaurant-delivery-relationships/specs/restaurant-delivery-relationships/spec.md)): после Revoke Staff пользователь теряет `restaurant#operate_kitchen`.

Общий каталог: [Restaurants use cases](../../../../README.md#use-cases).

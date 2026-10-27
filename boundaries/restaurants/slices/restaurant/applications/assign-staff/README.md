# Assign Staff

## Сценарий

- **Тип:** Command.
- **Actor:** Пользователь с `restaurant#manage` (владелец ресторана).
- **Намерение:** Дать пользователю доступ к кухне и меню ресторана.
- **Результат:** User id добавлен в множество `Restaurant.staff`; в SpiceDB записана связь `restaurant:<id>#staff@user:<userId>` (`TOUCH`), что даёт `manage_menu`, `operate_kitchen` и `view_orders`.

## Правила

- Без `restaurant#manage` — `PermissionDeniedError`, без записи и сохранения.
- Staff — идемпотентное множество: повтор — no-op (`changed: false`) без записи в SpiceDB.
- `TOUCH` выполняется до сохранения ресторана (FR-443). Ошибка SpiceDB (`AuthorizationUnavailableError`) отменяет команду; если запись прошла, а сохранение нет, лишнюю связь удалит reconciliation.

## Границы

- **Владелец:** `restaurants/slices/restaurant`.
- **Домен:** `domains/restaurant`.
- **Порты:** `RestaurantAccessControl.canManageRestaurant`, `StaffRelationshipWriter.touchStaff`, `RestaurantRepository`.
- **Transport:** RPC пока нет.

## Acceptance

AC-476 ([restaurant-delivery-relationships](../../../../../../openspec/changes/archive/2026-10-25-add-restaurant-delivery-relationships/specs/restaurant-delivery-relationships/spec.md)): после назначения пользователь получает `restaurant#operate_kitchen`.

Общий каталог: [Restaurants use cases](../../../../README.md#use-cases).

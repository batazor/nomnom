# Design

## RPC

| RPC | Request | Response | Право |
| --- | --- | --- | --- |
| `OpenRestaurant` | `restaurant_id` | `Restaurant restaurant`, `int64 version`, `bool changed` | `restaurant#manage` |
| `CloseRestaurant` | `restaurant_id` | `Restaurant restaurant`, `int64 version`, `bool changed` | `restaurant#manage` |
| `CreateMenu` | `restaurant_id` | `menu_id`, `restaurant_id`, `int64 version` | `restaurant#manage_menu` |
| `AddMenuItem` | `restaurant_id`, `name`, `description`, `Money price`, `optional bool available` | `menu_id`, `MenuItem menu_item`, `int64 version` | `restaurant#manage_menu` |
| `UpdateMenuItem` | `restaurant_id`, `menu_item_id`, `optional string name`, `optional string description`, `Money price`, `optional bool available` | `menu_id`, `MenuItem menu_item`, `int64 version`, `bool changed` | `restaurant#manage_menu` |
| `SetMenuItemAvailability` | `restaurant_id`, `menu_item_id`, `bool available` | `menu_id`, `MenuItem menu_item`, `int64 version`, `bool changed` | `restaurant#manage_menu` |

- Ответ команды — состояние после неё и `changed`, как у `AssignStaff`/`RevokeStaff`: повтор не отличается от успеха, и вызывающему не нужна отдельная обработка.
- `UpdateMenuItem`: незаданное поле не меняется (`optional` и отсутствие `price`); пустой `description` удаляет описание — так нормализует domain.
- `AddMenuItem` генерирует id блюда на сервере и не дедуплицирует повтор: idempotency key — отдельное решение, BFF не повторяет команду после ответа.
- `version` — версия aggregate после команды (`Restaurant` или `Menu`); при `changed = false` — текущая.

## Reconciler: версия ресторана

Порт `RestaurantMemberships` отдаёт `version` документа вместе с владельцем и персоналом. Missing-кандидат хранит `since` и `version`; проход восстанавливает связь, только если прошло не меньше grace и `version` совпала. Любая другая версия заменяет кандидата новым с текущим временем.

Почему достаточно версии: Revoke Staff сохраняет ресторан с новой версией, Assign Staff — тоже. Последовательность revoke(saved) → assign(saved) → revoke(in flight) между проходами меняет версию минимум дважды, поэтому второй проход не совпадает с первым. Ложные сбросы (переименование, Open/Close) лишь откладывают восстановление на grace.

`recordAuthorizationRevision` теперь возвращает `{ from, to }`. Если `from` равна версии, прочитанной этим проходом, запись изменила только revision, и кандидаты этого ресторана с версией `from` переносятся на `to`. Без этого восстановление owner меняло бы версию до проверки staff, и одновременная потеря обеих связей занимала бы лишний grace. При конкурентном сохранении (`from` другая) перенос не делается.

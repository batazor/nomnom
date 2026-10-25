# Proposal

## Why

Handlers Open Restaurant, Close Restaurant, Create Menu, Add Menu Item, Update Menu Item и Set Menu Item Availability реализованы и пишут свои факты в Outbox (`restaurants.v1.restaurant-events`, `restaurants.v1.menu-events`), но не подключены к transport: BFF не может открыть ресторан или изменить меню от имени управляющего, а события `RestaurantOpened`, `RestaurantClosed` и `MenuItemAvailabilityChanged` в работающей системе не возникают.

Reconciler `restaurants` восстанавливает missing `restaurant#owner`/`restaurant#staff` после двух проходов с интервалом не меньше grace (FR-445). Если между проходами пользователя снова назначили и снова сняли, а второй Revoke Staff уже сделал `DELETE`, но ещё не сохранил ресторан, второй проход видит того же пользователя missing и восстанавливает только что отозванный доступ: правило сравнивает только пару ресторан–пользователь, а не состояние ресторана.

## What Changes

- **gRPC API `restaurants`** (аддитивно в `service.proto`): `OpenRestaurant`, `CloseRestaurant` (`restaurant#manage`), `CreateMenu`, `AddMenuItem`, `UpdateMenuItem`, `SetMenuItemAvailability` (`restaurant#manage_menu`). Повтор Open/Close и команды меню с текущими значениями — успех с `changed = false` без записи Outbox; повторный `CreateMenu` — `ALREADY_EXISTS`; `AddMenuItem` не идемпотентен. Ошибки — по общему маппингу (`invalid_menu`, `invalid_money` → `INVALID_ARGUMENT`). Handlers регистрируются в Awilix слайсов `restaurant` и `menu`.
- **Internal gateway:** route `restaurants-v1-management` с этими методами и политикой `commands`.
- **Reconciler:** missing-кандидат привязан к версии документа `Restaurant`; восстановление — только если оба прохода видели ту же версию. Другая версия начинает grace заново. Собственная запись revision reconciler'а после восстановления другой relation переносит кандидатов ресторана на новую версию, чтобы одновременная потеря owner и staff восстанавливалась за те же два прохода.
- SpiceDB schema не меняется: `manage` и `manage_menu` уже есть.

Не входит: Update Restaurant по gRPC, idempotency key для `AddMenuItem`, публикация `RegisterRestaurant`/`AssignStaff`/`RevokeStaff` в gateway.

## Capabilities

### New Capabilities

Нет.

### Modified Capabilities

- `restaurant-administration-api`: RPC открытости ресторана и управления меню.
- `restaurant-delivery-relationships`: восстановление missing relationship в `restaurants` учитывает версию ресторана (FR-445).

## Impact

`boundaries/restaurants`: `infrastructure/grpc` (`service.proto`, `restaurant-commands.ts`, `menu-commands.ts`, `errors.ts`, `messages.ts`, `server.ts`), `slices/restaurant/di`, `slices/menu/di`, `di/restaurants.ts`, `slices/restaurant/infrastructure/reconcile`, memberships MongoDB/in-memory, тесты, README. `ops/kind/manifests/gateway/gateway.yaml`, `ops/kind/README.md`. Клиент — BFF.

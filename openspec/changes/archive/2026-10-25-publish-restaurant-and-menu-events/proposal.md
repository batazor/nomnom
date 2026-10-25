# Proposal

## Why

`restaurant-management` и `restaurant-menu` требуют, чтобы Open/Close Restaurant и Set Menu Item Availability (и Update Menu Item при смене доступности) атомарно сохраняли `RestaurantOpened`, `RestaurantClosed` и `MenuItemAvailabilityChanged` в Outbox. Use cases уже передают эти Domain Events в порт Outbox внутри Unit of Work, но у них нет Protobuf-контракта: единственный mapper `restaurants` (Kitchen Order) их пропускает, и `integration-event-publication` прямо запрещает их публикацию (AC-603). Требования противоречат друг другу, а факты не покидают контекст.

## What Changes

- **Контракты** в `nomnom/restaurants/v1/events.proto` (только добавление): `RestaurantOpened` и `RestaurantClosed` (`restaurant_id`), `MenuItemAvailabilityChanged` (`menu_id`, `restaurant_id`, `menu_item_id`, `available`).
- **Отдельный стрим на aggregate**, чтобы порядок по `aggregate_id` сохранялся внутри стрима: `restaurants.v1.restaurant-events` (`aggregate_type = Restaurant`, key — id ресторана) и `restaurants.v1.menu-events` (`aggregate_type = Menu`, key — id меню). `restaurants.v1.restaurant-order-events` не меняется.
- **Mappers** со явным allowlist в `slices/restaurant/infrastructure/integration` и `slices/menu/infrastructure/integration`; mapper процесса диспетчеризует по `aggregate_type` к mapper слайса (Awilix: каждый слайс регистрирует свой mapper, `di/restaurants.ts` их компонует). `event_id`, Event Envelope, relay — без изменений.
- Остальные события `Restaurant` (`RestaurantRegistered`, `RestaurantRenamed`, `RestaurantAddressChanged`) и `Menu` (`MenuCreated`, `MenuItemAdded`, `MenuItemUpdated`) остаются внутренними; назначение и отзыв staff — relationships SpiceDB, Domain Events не порождают.

Не входит: transport (RPC) для Open/Close Restaurant и команд меню, consumers новых стримов, Kafka ACL. Topics создаются брокером при первой публикации (как `restaurant-order-events`); списка topics в ops нет.

## Capabilities

### New Capabilities

Нет.

### Modified Capabilities

- `integration-event-publication`: allowlist `restaurants` включает факты открытости ресторана и доступности Menu Item; два новых стрима.

## Impact

`boundaries/restaurants` (`events.proto` и генерация, mappers слайсов `restaurant`, `menu`, общий `pkg/infrastructure/outbox/integration-event.ts`, DI, тесты, README), `docs/domain-events.md`.

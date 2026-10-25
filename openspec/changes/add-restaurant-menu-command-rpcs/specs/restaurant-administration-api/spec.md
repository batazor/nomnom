# Spec Delta

## ADDED Requirements

### Requirement: Открытость ресторана управляется по gRPC
**ID:** FR-570

`restaurants` SHALL публиковать `OpenRestaurant` и `CloseRestaurant` с `restaurant_id`; оба MUST требовать `restaurant#manage` и выполнять Open Restaurant и Close Restaurant (FR-203), записывая `RestaurantOpened`/`RestaurantClosed` в Outbox той же Unit of Work. Ответ SHALL содержать ресторан, его версию и `changed`; повтор MUST быть успешным с `changed = false` без записи Outbox. Методы SHALL быть опубликованы в internal gateway с политикой команд.

#### Scenario: Управляющий открывает ресторан
**ID:** AC-711

- **WHEN** управляющий вызывает `OpenRestaurant` закрытого ресторана с `traceparent`
- **THEN** ответ содержит открытый ресторан, `version = 2`, `changed = true`, а Outbox — одну запись `RestaurantOpened` в `restaurants.v1.restaurant-events` с `traceparent` того же trace

#### Scenario: Повтор открытия
**ID:** AC-712

- **WHEN** `OpenRestaurant` вызван для открытого ресторана
- **THEN** ответ успешен с `changed = false`, новая запись Outbox не создаётся

#### Scenario: Без права manage
**ID:** AC-713

- **WHEN** пользователь без `restaurant#manage` вызывает `OpenRestaurant` или `CloseRestaurant`
- **THEN** вызов отклоняется `PERMISSION_DENIED`, ресторан и Outbox не меняются

### Requirement: Меню управляется по gRPC
**ID:** FR-571

`restaurants` SHALL публиковать `CreateMenu`, `AddMenuItem`, `UpdateMenuItem` и `SetMenuItemAvailability` в internal gateway; все MUST требовать `restaurant#manage_menu` и выполнять use cases FR-211, FR-213…FR-215. Ответы команд над блюдом SHALL содержать `menu_id`, блюдо и версию меню, а `UpdateMenuItem` и `SetMenuItemAvailability` — `changed`. Ошибки use cases MUST маппиться по общему правилу: already exists, not found, invalid argument.

#### Scenario: Блюдо становится недоступным
**ID:** AC-714

- **WHEN** управляющий вызывает `SetMenuItemAvailability` с `available = false` для доступного блюда, затем повторяет вызов
- **THEN** первый ответ — `changed = true` и Outbox содержит `MenuItemAvailabilityChanged` в `restaurants.v1.menu-events`; повтор — `changed = false` без новой записи

#### Scenario: Без права manage_menu
**ID:** AC-715

- **WHEN** пользователь без `restaurant#manage_menu` вызывает любую команду меню
- **THEN** вызов отклоняется `PERMISSION_DENIED`, меню и Outbox не меняются

#### Scenario: Меню уже создано
**ID:** AC-716

- **WHEN** `CreateMenu` вызывается для ресторана, у которого есть меню
- **THEN** вызов отклоняется `ALREADY_EXISTS`

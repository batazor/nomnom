# Design

## Стримы

Один topic на aggregate. Relay публикует записи в порядке `seq` с key = `aggregate_id`, поэтому порядок гарантирован только внутри partition одного topic. `Restaurant` и `Menu` — разные aggregates с разными id и версиями; общий topic смешал бы ключи разной природы (id ресторана и id меню) без пользы для порядка. Kitchen Order остаётся в `restaurants.v1.restaurant-order-events`.

| Topic | `aggregate_type` | Key | Integration Events |
| --- | --- | --- | --- |
| `restaurants.v1.restaurant-order-events` | `RestaurantOrder` | id Kitchen Order | 7 фактов Kitchen Order (FR-524) |
| `restaurants.v1.restaurant-events` | `Restaurant` | id ресторана | `RestaurantOpened`, `RestaurantClosed` |
| `restaurants.v1.menu-events` | `Menu` | id меню | `MenuItemAvailabilityChanged` |

`MenuItemAvailabilityChanged` несёт и `menu_id`, и `restaurant_id`: consumer, которому нужен ресторан, не обращается к `restaurants`.

## Mapper процесса

`MongoOutbox` принимает один `OutboxEventMapper`. `outboxEventMapperByAggregate` выбирает mapper слайса по `aggregate_type` (неизвестный тип — не публикуется); mapper слайса дополнительно проверяет свой `aggregate_type` и allowlist по собственным ключам (`Object.hasOwn`). Общие части — `protobufIntegrationEvents(topic)` и `allowlistMapper` в `pkg/infrastructure/outbox/integration-event.ts`; mapper Kitchen Order переведён на них без изменения поведения.

## Ограничение event_id

`event_id` — UUIDv5 от `aggregate_type:aggregate_id:aggregate_version:event_type`. Сейчас каждая команда меню меняет доступность не более одного Menu Item за версию, поэтому дубля нет. Команда, меняющая доступность нескольких позиций в одной UoW, получила бы одинаковый `event_id` и откат транзакции (FR-536) — для неё потребуется отдельное решение (свёртка N → 1 или иной ключ).

## Relay и метрики

Relay уже работает по topic записи: backlog группируется по `topic`, метрики помечены `messaging.destination.name`, lease один на процесс. Изменений не требуется.

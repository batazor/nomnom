# Proposal

## Why

Use cases bounded context `restaurants` описаны в `boundaries/restaurants/README.md`, [Kitchen](../../../docs/kitchen.md) и [FSM](../../../docs/fsm.md), но в коде есть только две чистые Specifications (`RestaurantAcceptingOrders`, `MenuItemAvailable`). Без application-слоя `orders` не может опереться на синхронный контракт Receive/Cancel Restaurant Order, а следующие шаги (gRPC-адаптер, MongoDB, Outbox publisher) не имеют прикладных контрактов, к которым подключаться.

## What Changes

- Добавить минимальные доменные модели, недостающие для use cases: aggregate `Restaurant` (рядом с существующей policy), aggregate `Menu` с entity `MenuItem` и value object `Money` на `decimal.js`, aggregate `RestaurantOrder` с чистой FSM в виде transition table, value object `Quantity`, domain service проверки Acceptance и Domain Events с `pullEvents()`.
- Добавить application handlers (Command/Query → Handler → Result) для use cases:
  - `slices/restaurant`: Register Restaurant, Update Restaurant, Open Restaurant, Close Restaurant, Get Restaurant, List Restaurants;
  - `slices/menu`: Create Menu, Add Menu Item, Update Menu Item, Set Menu Item Availability, Get Menu;
  - `slices/kitchenorder`: Receive Order Request, Accept Order (вместе с Queue в одной UoW), Reject Order, Queue Restaurant Order, Start Preparation, Mark Assembled, Mark Ready for Pickup, Report Preparation Delay, Report Preparation Failure, Cancel Restaurant Order, Get Restaurant Order, Get Kitchen Queue, List Active Preparations, List Ready Orders.
- Объявить порты: shared `UnitOfWork`, `Outbox`, `Clock`, `IdGenerator`, `Subject` и application errors в `pkg/application`; узкие authorization ports в `applications/` каждого слайса; контракты repositories в `domains/`; query/read-model ports и ports для открытых продуктовых решений (условия Acceptance, расчёт `expected_ready_at`, порядок Kitchen Queue) рядом с use cases.
- Добавить in-memory адаптеры всех портов (с транзакционным откатом и optimistic concurrency) в `infrastructure/memory` и unit tests на каждый handler, FSM и новые доменные модели.
- Расширить `biome.json` на `cmd/`, `pkg/`, `slices/`, добавить runtime-зависимость `decimal.js` (зафиксирована в `boundaries/README.md` как десятичный тип `Money` для `restaurants`).
- Обновить статус реализации в `boundaries/restaurants/README.md`; добавить README для новых use-case packages.

### Исключено из scope

- **Start / Complete Preparation Item** — существуют только при item-level model; [Kitchen](../../../docs/kitchen.md#preparation-item-lifecycle) рекомендует для MVP вариант A без lifecycle позиций. Mark Assembled остаётся утверждением кухни о готовности всех позиций.
- **Reject After Acceptance** — в MVP не разрешён до продуктового решения; FSM возвращает `InvalidTransition` для `ACCEPTED/QUEUED × Reject`.
- Courier projection в List Ready Orders, tombstone для Cancel до Receive, modifiers/options как модель меню, атрибуты ресторана сверх `name`, часы работы и capacity как данные aggregate — открытые вопросы; поведение закрыто портами или не моделируется (см. design.md).
- gRPC/Protobuf, HTTP, MongoDB, Kafka, SpiceDB adapters, Integration Event mappers, BDD `.feature`/Cucumber.js, Helm/ops и изменения `cmd/`.

## Capabilities

### New Capabilities

- `restaurant-management`: регистрация ресторана, изменение основных данных, открытие и закрытие для новых Restaurant Order, публичные Get/List Restaurants.
- `restaurant-menu`: создание меню ресторана, добавление и изменение Menu Item, изменение доступности, чтение актуального меню.
- `restaurant-order-lifecycle`: получение Restaurant Order от `orders`, Acceptance/Rejection, Kitchen Queue, Preparation, Assembly, Ready for Pickup, Preparation Delay/Failure, Cancellation по стадиям и чтение одного Restaurant Order.
- `kitchen-board`: read models кухни — Kitchen Queue, активные Preparations и готовые к Pickup заказы.

### Modified Capabilities

Нет.

## Impact

- Код: `boundaries/restaurants/pkg/application`, `pkg/infrastructure/memory`, `slices/{restaurant,menu,kitchenorder}/{domains,applications,infrastructure/memory}`, `test/`.
- Зависимости: `decimal.js` (runtime, последняя стабильная `10.6.0`).
- Проверки: `npm test`, `npm run build`, `npm run check` в `boundaries/restaurants`; `biome check` начинает покрывать production-код.
- Внешние контракты, deployment и `cmd/` не меняются; HTTP-каркас продолжает работать без wiring handlers.

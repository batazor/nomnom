# Design

## Текущие обратные связи

| Связь | Импорт | Что с ним происходит |
| --- | --- | --- |
| `restaurant → menu` | `domains/menu/vo/money.ts` в `restaurant.ts`, `update-restaurant.ts`, `restaurant-view.ts` | Файл переезжает |
| `restaurantorder → cook` | `CookWorkload` в `repository/restaurant-orders.ts`, `CookRestaurants` в `restaurant-facts/restaurant-facts.ts` | Убирается `implements` |
| `restaurantorder → simulator` | `PendingKitchenOrder`, `PendingKitchenOrdersReader`, `SIMULATED_STATES` в `repository/restaurant-orders.ts` | Запрос становится параметризованным |

Остальные импорты между слайсами уже идут в целевых направлениях и не меняются.

## `Money` в `restaurant`

`Money` переезжает в `slices/restaurant/domains/restaurant/vo/money.ts`. `menu` уже зависит от `restaurant` (`MarketId`, `RestaurantRepository`), поэтому новой связи не возникает. Общий пакет не создаётся: ADR 6 запрещает выносить домен в `pkg/`, а новый слайс ради одного value object не оправдан. Правила `Money` и его тесты не меняются.

## `cook` и `restaurantorder`

Порты `CookWorkload` и `CookRestaurants` остаются у потребителя в `cook/applications/cook-workload` (ADR 6). Адаптеры уже подставляются из cradle: `cook/di` получает `kitchenBoard` и `restaurantExistence`. TypeScript сравнивает типы структурно, поэтому `implements` не нужен:

- `DatabaseKitchenBoard` оставляет методы `activeAssignments` и `queuedOrders`;
- `RepositoryRestaurantExistence` оставляет метод `exists`;
- несовместимость сигнатур ловит `tsc`: `CookDependencies` и `RestaurantOrderCradle` объединяются в cradle процесса с общими ключами.

Цена: класс больше не объявляет, какой порт `cook` он реализует. Комментарий рядом с методами называет потребителя.

Отдельный адаптер в `cook/infrastructure` не вводится: он только передавал бы вызовы в те же методы.

## `simulator` и `restaurantorder`

Запрос читает коллекцию Restaurant Orders, поэтому он остаётся в репозитории `restaurantorder`. Состояния и форму результата задаёт Simulator:

- `restaurantorder/applications/kitchen-board` объявляет `listKitchenOrdersInStates(states)` и тип результата: `restaurantOrderId`, `restaurantId`, `marketId`, `state`, `expectedReadyAt`, `scheduled`;
- `DatabaseKitchenBoard` реализует его тем же фильтром, сортировкой и индексом `(state, receivedAt)`, что и сегодняшний `listPendingKitchenOrders`;
- новый адаптер `simulator/infrastructure/pending-kitchen-orders.ts` реализует порт `PendingKitchenOrdersReader`: вызывает запрос с `SIMULATED_STATES` и сужает `state` до `SimulatedState`;
- `simulator/di` передаёт этот адаптер в `KitchenSimulator` вместо `kitchenBoard`.

Для Simulator поведение не меняется.

## Проверка направлений

В `.dependency-cruiser.cjs` на каждый слайс появляется правило вида:

```js
{
  name: "slice-menu-dependencies",
  severity: "error",
  from: { path: "^slices/menu/" },
  to: { path: "^slices/", pathNot: "^slices/(menu|restaurant)/" },
}
```

Правила охватывают и `di` слайса: там тоже есть импорты соседей. `npm run architecture` проходит по `cmd di infrastructure pkg slices`, поэтому тесты из `test/` под правила не попадают.

Текущий конфиг dependency-cruiser с парсером swc уже видит `import type`: у `assign-cook.ts` импорт `start-preparation.ts` определён с типом `type-only`. Значит, отдельная настройка не нужна, а правила поймают и порты, и домены.

## Порядок правок

Сначала разрываются связи, затем добавляются правила, иначе `npm run check` падает в середине работы. Проверка выполняется один раз в конце, включая намеренную обратную связь, которую линтер должен отвергнуть (`AC-994`).

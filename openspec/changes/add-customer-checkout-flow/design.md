# Design

## Context

- `placeorder.Catalog.Snapshot(restaurantID, menuItemIDs) → CatalogSnapshot{Currency, Items{id: {UnitPrice, Available}}, MinimumTotal, MaximumTotal}`; отсутствующие позиции считаются недоступными; `ErrUnknownRestaurant` — ресторан без меню. Политика `OrderItemsOrderable` даёт причины `order_item_unavailable` (с `MenuItemID`), `order_total_below_minimum`, `order_total_above_maximum`.
- `orders` уже вызывает соседей через internal gateway (`slices/fulfillment/infrastructure/neighbours/grpcclient`) с service token; клиент `restaurants` там сгенерирован.
- BFF: identity (ADR 32), слайс `ordertracking` и генерация клиентов из workspace (`plugins/bff/infrastructure/grpc/buf.gen.yaml`).
- UI: контракт и MSW-моки всех customer-операций уже есть (`src/shared/graphql/mocks`), экран `/orders/:orderId` готов.

## Decisions

### 1. Каталог orders

Адаптер `slices/order/infrastructure/catalog` (Go) вызывает `GetRestaurant` (`is_open`) и `GetMenu`, строит snapshot по запрошенным позициям. Закрытый ресторан — `placeorder.ErrRestaurantNotAcceptingOrders` до транзакции; `NOT_FOUND` меню или ресторана — `ErrUnknownRestaurant`; недоступность `restaurants` — `application.ErrDependencyUnavailable`. Валюта — валюта меню; позиции разных валют — отказ `INVALID_ORDER`. Минимум и максимум суммы — `catalog.minimum_total` и `catalog.maximum_total` (`CATALOG__MINIMUM_TOTAL`, `CATALOG__MAXIMUM_TOTAL`, по умолчанию `0` и `100000`, в валюте меню) как placeholders до продуктового решения. Соединение — тот же lazy gRPC client к internal gateway, что у fulfillment neighbours; `UnavailableCatalog` остаётся для тестов и явного отключения.

### 2. Причины отказа PlaceOrder

| Ошибка | gRPC | `ErrorInfo.reason` | metadata |
| --- | --- | --- | --- |
| `order.NotOrderable` | `FAILED_PRECONDITION` | `ORDER_NOT_ORDERABLE` | `codes` (через запятую, верхний регистр), `menu_item_ids` (через запятую) |
| `placeorder.ErrRestaurantNotAcceptingOrders` | `FAILED_PRECONDITION` | `RESTAURANT_NOT_ACCEPTING_ORDERS` | — |

Остальные ошибки ввода остаются `INVALID_ARGUMENT` / `INVALID_ORDER`.

### 3. restaurants: ListRestaurants

`rpc ListRestaurants(ListRestaurantsRequest{page_size, page_token, include_closed}) returns (ListRestaurantsResponse{repeated Restaurant restaurants, next_page_token})` поверх use case, без authorization.

### 4. BFF

```text
plugins/bff/
  catalog/      applications (List, Get с меню), infrastructure (restaurants)
  checkout/     applications (PlaceOrder → GetOrder), infrastructure (orders)
```

- `restaurants(first, after)` → `ListRestaurants`; курсор — `next_page_token` как есть; `menu` в списке не заполняется.
- `restaurant(id)` → `GetRestaurant` + `GetMenu`; `NOT_FOUND` ресторана — `null`, нет меню — `menu = null`.
- `placeOrder` требует access token; `PlaceOrder` от имени покупателя, затем `GetOrder` того же заказа и представление `ordertracking` (переиспользуется сценарий `GetOrder`); `FAILED_PRECONDITION` с `ORDER_NOT_ORDERABLE` — `problems` из metadata, `RESTAURANT_NOT_ACCEPTING_ORDERS` — problem без позиций; `order = null` при problems.
- Каталог публичный: resolvers каталога не требуют access token.

### 5. UI

- `browse-restaurants` (`/`): список с пагинацией «показать ещё», закрытые рестораны не показываются (фильтр по умолчанию).
- `view-restaurant` (`/restaurants/:restaurantId`): меню, недоступные позиции видны, но не добавляются; корзина — локальное состояние в `localStorage` по ресторану (обёрнуто в try/catch), количество, итог считается только как подсказка — авторитетная сумма приходит из `placeOrder`.
- `place-order`: адрес доставки, кнопка «Оформить»; `idempotencyKey` создаётся на попытку и переиспользуется при ручном retry; `problems` показываются у позиций и над формой; успех — очистка корзины и переход на `/orders/:id`.
- Preview `customer-journey-preview` удаляется; навигация шапки ведёт на `/`.
- Моки уже обслуживают `restaurants`, `restaurant`, `placeOrder`; feature documents входят в contract-тест.

## Risks / Trade-offs

- [Placeholder лимитов суммы] → в конфигурации и README помечены как placeholders.
- [Сумма корзины в UI может разойтись с серверной] → UI показывает её как предварительную, итог заказа — из ответа.
- [Нет e2e через gateway, пока BFF не в kind] → unit-тесты адаптеров на bufconn, UI — на MSW; e2e — в change развёртывания BFF.

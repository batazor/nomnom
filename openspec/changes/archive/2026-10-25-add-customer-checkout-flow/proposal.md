# Proposal

## Why

Экран отслеживания заказа работает от UI до контекстов, но заказ негде оформить:

- в `orders` каталог Place Order (`placeorder.Catalog`) — заглушка `UnavailableCatalog`, поэтому `PlaceOrder` всегда отвечает `UNAVAILABLE`; она ждала API меню `restaurants`, а `GetMenu` теперь опубликован;
- `PlaceOrder` отдаёт все отказы политики `OrderItemsOrderable` одним `ErrorInfo.reason = INVALID_ORDER` без кода причины и позиций, а схема BFF требует `problems` с кодами и `menuItemIds`;
- в `restaurants` нет RPC `ListRestaurants`, хотя use case List Restaurants реализован;
- в BFF `restaurants`, `restaurant` и `placeOrder` объявлены в схеме, но не реализованы;
- в UI вместо каталога, корзины и оформления — fixture-preview на `/`.

## What Changes

- **restaurants**: RPC `ListRestaurants` (публичный, по умолчанию открытые рестораны, курсор) поверх use case.
- **orders**:
  - production-адаптер `placeorder.Catalog` поверх `restaurants.GetRestaurant` и `GetMenu` через internal gateway (service identity): цены и доступность позиций, закрытый ресторан — новый отказ `RESTAURANT_NOT_ACCEPTING_ORDERS`; минимальная и максимальная сумма заказа — из конфигурации (`catalog.minimum_total`, `catalog.maximum_total`, placeholders), пока `restaurants` их не публикует;
  - `PlaceOrder` при `NotOrderable` возвращает `FAILED_PRECONDITION` с `ErrorInfo.reason = ORDER_NOT_ORDERABLE` и metadata `codes` и `menu_item_ids`; закрытый ресторан — `ErrorInfo.reason = RESTAURANT_NOT_ACCEPTING_ORDERS`.
- **BFF**, слайсы `catalog` и `checkout`:
  - `restaurants(first, after)` и `restaurant(id)` с меню — публичные чтения `restaurants`, без проверки покупателя;
  - `placeOrder(input)` — от имени покупателя: `PlaceOrder` с `idempotencyKey`, затем `GetOrder` для payload; отказы — `problems`.
- **UI**: production-features вместо preview — `browse-restaurants` на `/`, `view-restaurant` на `/restaurants/:id` с корзиной (локальное состояние браузера, одна корзина на ресторан), `place-order` (адрес, idempotency key на попытку, ручной retry с тем же ключом, problems у позиций) с переходом на `/orders/:id`. Fixture-preview удаляется.

Не входит: история заказов (`customerOrders`) — `OrderSummary` не содержит позиций и истории; оплата и выбор способа оплаты; поиск и фильтры каталога; серверная корзина.

## Capabilities

### New Capabilities

Нет.

### Modified Capabilities

- `restaurant-management`: List Restaurants доступен по gRPC.
- `order-use-cases`: Place Order читает каталог из `restaurants` и называет причины отказа.
- `customer-order-resolvers`: каталог и оформление заказа в BFF.
- `customer-web-application`: каталог, корзина и оформление заказа вместо preview.

## Impact

`boundaries/restaurants` (proto, сервис, тесты), `boundaries/orders` (адаптер каталога, конфигурация, DI, gRPC-ошибки, Helm), `boundaries/bff/plugins/bff` (слайсы, resolvers, генерация клиентов), `boundaries/ui` (features, router, i18n, моки), README. Контракты расширяются обратно совместимо.

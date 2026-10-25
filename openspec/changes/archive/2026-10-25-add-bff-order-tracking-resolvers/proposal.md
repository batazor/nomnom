# Proposal

## Why

Экран отслеживания заказа в customer UI работает только на MSW-моках: в BFF операции `order(id)` и `requestOrderCancellation` объявлены в схеме, но возвращают `Unimplemented`. Всё, что нужно для их реализации, уже есть или почти есть: `orders` публикует `GetOrder` с окном отмены и `RequestOrderCancellation`, user identity передаётся BFF-signed assertions (ADR 32). Не хватает трёх вещей:

- в `restaurants` нет RPC `GetMenu`, поэтому BFF не может показать названия блюд, хотя меню — публичные данные (FR-216);
- `orders` отдаёт все отказы Cancellation Request одним `ErrorInfo.reason = ORDER_STATE_CONFLICT`, и BFF не может отличить закрытое окно отмены от уже завершённого заказа, а схема требует разных `OrderCancellationProblemCode`;
- у BFF нет gRPC-клиентов к контекстам.

## What Changes

- **restaurants**: RPC `GetMenu(restaurant_id)` поверх существующего use case Get Menu; без authorization, как и Get Restaurant.
- **orders**: отказ Cancellation Request по окну возвращает `FAILED_PRECONDITION` с `ErrorInfo.reason` `CANCELLATION_WINDOW_NOT_OPEN` или `CANCELLATION_WINDOW_EXPIRED`; отказ по состоянию или процессу — прежний `ORDER_STATE_CONFLICT`.
- **BFF**, слайс `order-tracking`:
  - gRPC-клиенты `orders` и `restaurants`, сгенерированные из workspace (как в `orders`, до публикации в BSR), соединение через internal gateway (`BFF__INTERNAL_GATEWAY_ADDR`) с interceptor ADR 32;
  - resolver `order(id)`: проверка access token покупателя, `GetOrder` от имени покупателя, `GetRestaurant` и `GetMenu` для названий; customer-представление — `OrderStatus`, `endReason`, milestones, `lineTotal`, `cancellation.availableUntil`;
  - resolver `requestOrderCancellation`: `RequestOrderCancellation` от имени покупателя, перевод исхода и отказов в `outcome` / `problem`.
- Документация BFF и UI (экран заказа больше не только на моках).

Не входит: остальные customer-операции (`restaurants`, `restaurant`, `customerOrders`, `placeOrder`), развёртывание BFF в kind и e2e через gateway, кеширование меню.

## Capabilities

### New Capabilities

- `customer-order-resolvers`: resolvers BFF для отслеживания и отмены заказа.

### Modified Capabilities

- `restaurant-menu`: Get Menu публикуется по gRPC.
- `order-use-cases`: отказ Cancellation Request различает причину в транспортной ошибке.

## Impact

- `boundaries/restaurants/infrastructure/grpc` (proto, сервис, тесты), `boundaries/orders/infrastructure/grpc/service.go`.
- `boundaries/bff/plugins/bff`: новый слайс, сгенерированные клиенты, конфигурация, `src/main.go`; Helm values BFF (адрес gateway).
- Протоколы: добавление RPC и значений `ErrorInfo.reason`, обратно совместимо.

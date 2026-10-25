# Proposal

## Why

README `orders` перечисляет как нереализованные остальные command RPC и writers relationships `restaurant`/`courier`/`delivery` с их reconciliation. Сейчас gRPC API отдаёт только `GetOrder` и `ListCustomerOrders`, хотя handlers Place Order и Request Order Cancellation готовы, а в authorization graph заказа есть только `order#customer`: ресторан и назначенный курьер не могут прочитать заказ, с которым работают, а `docs/security.md` уже называет `order#restaurant` creation-time association заказа.

## What Changes

- В `OrdersService` добавляются RPC `PlaceOrder` и `RequestOrderCancellation`. Только у этих use cases есть API callers: Confirm/Reject/Record* приходят из Kafka, Complete/Fail or Cancel Order вызывает только Temporal workflow, Relay Preparation Issue — только Kafka; они в API не публикуются. Customer берётся из verified subject, а не из поля запроса.
- API получает lazy Temporal client (старт не зависит от Temporal), системный clock, генератор UUIDv7 `order_id` и fail-closed catalog: у `restaurants` нет RPC меню, поэтому Place Order отвечает `UNAVAILABLE`/`DEPENDENCY_UNAVAILABLE`. Request Order Cancellation получает тот же экземпляр cancellation policy, что и Get Order (FR-462).
- Маппинг ошибок дополняется `application.ErrDependencyUnavailable` (его оборачивает `spicedb.ErrUnavailable`), ошибками состава заказа (`INVALID_ORDER`), state conflict и отказа workflow (`ORDER_STATE_CONFLICT`).
- `definition order` получает relations `restaurant`, `courier`, `delivery`; `view = customer + restaurant->view_orders + courier + delivery->act_as_courier`, `cancel` остаётся только у customer.
- Place Order пишет `order#customer` и `order#restaurant` одним `WriteRelationships`.
- `orders-consumer` пишет `order#courier` и `order#delivery` из `CourierAssignedToDelivery` внутри Inbox unit of work до commit; назначение хранится в `orders` (миграция `00002`). Reassignment — один атомарный запрос `TOUCH` нового и `DELETE` прежнего курьера; событие старше сохранённого назначения пропускается.
- Reconciler обобщается на четыре relation с тем же двухпроходным grace-алгоритмом.

Не входит: user identity через BFF (PlaceOrder для service subject отвечает `PERMISSION_DENIED`), API меню `restaurants`, снятие курьера без замены — `delivery` не публикует `CourierUnassignedFromDelivery`/`DeliveryCancelled`, поэтому такое снятие не наблюдаемо.

## Capabilities

### New Capabilities

Нет.

### Modified Capabilities

- `order-use-cases`: gRPC RPC Place Order и Request Order Cancellation, маппинг ошибок.
- `spicedb-authorization`: relations `order#restaurant`, `order#courier`, `order#delivery` и их writers.
- `order-relationship-reconciliation`: reconciliation всех relations `order`.

## Impact

- `boundaries/orders`: `service.proto` и gen, `infrastructure/grpc`, `pkg/application`, `slices/order/applications/{placeorder,recordcourierassignment,fulfillment}`, `slices/order/infrastructure/{spicedb,postgres,memory,reconcile,neighbours,system,messaging}`, `slices/order/di`, `di`, миграция `00002_courier_assignment.sql`, README.
- `authorization/schema.zed`, `authorization/validation.yaml`, `docs/security.md`.
- `orders-consumer` теперь требует `SPICEDB__ENDPOINT`; API — конфигурацию `temporal.*` и `fulfillment.*`. GRPCRoute `orders-v1` покрывает весь service, ops не меняются.
- Новые поля и RPC обратно совместимы по `buf breaking`.

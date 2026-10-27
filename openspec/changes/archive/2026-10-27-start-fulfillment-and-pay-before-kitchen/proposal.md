# Proposal

## Why

Place Order запускает Order Fulfillment Workflow портом после commit (`placeorder.go`, `fulfillment.Starter`). Ошибка старта видна только как `fulfillment_started = false`. Если процесс упадёт между commit и стартом, у заказа навсегда не будет оркестрации: consumer собственного `OrderPlaced` из ADR 5 так и не написан, хотя `SignalWithStart` уже есть в адаптере (`StartFromOrderPlaced`) и его никто не вызывает.

Кроме того, `restaurants` создаёт Restaurant Order сразу из `OrderPlaced`, а workflow сначала авторизует оплату. Из-за этого возникают две проблемы:

- кухня может принять заказ раньше шага `RequestRestaurantOrder`. Workflow отклоняет такой факт как «restaurant order not requested», помечает его обработанным, а потом падает по таймауту приёмки;
- при отказе оплаты Saga не отменяет Restaurant Order (`restaurantRequested = false`), а `OrderCancelled` restaurants не читает. Неоплаченный заказ остаётся на кухне, и кухня может его приготовить.

Наконец, покупатель и флоу-борд не видят оплату: `orders` не хранит факт авторизации, и в карточке полного цикла нет такого шага.

## What Changes

- **Старт только по `OrderPlaced`.** В `orders` появляется use case Start Fulfillment: consumer группы `orders` на `orders.v1.order-events`, Inbox и чтение Order в одной UoW. Если Order ещё не завершён, после commit выполняется `SignalWithStart(order:<order_id>, OrderPlaced)`, в том числе на дубликате Inbox. Ошибка Temporal считается временной: Kafka доставит сообщение повторно, в DLQ оно не уходит. Собственные `OrderCompleted` и `OrderCancelled` группа `orders` подтверждает явным no-op.
- **Place Order не обращается к Temporal.** Порт старта и `Result.FulfillmentStarted` удаляются. **BREAKING:** поле `PlaceOrderResponse.fulfillment_started = 3` удаляется, номер и имя помечаются `reserved`.
- **Кухня получает заказ только после оплаты.** `restaurants` больше не создаёт Restaurant Order из `OrderPlaced`: consumer `OrderPlaced` и его маршрут в `restaurants-consumer` удаляются. Restaurant Order создаёт только шаг workflow `RequestRestaurantOrder` (RPC `ReceiveOrderRequest`) после авторизации оплаты. Процесс `restaurants-consumer` остаётся: он читает `delivery.v1.delivery-events` и обновляет кеш каталога.
- **Оплата видна в Order.** После одобрения `AuthorizePayment` workflow выполняет шаг `RecordPaymentAuthorization`. Activity вызывает use case Record Payment Authorization слайса `order`: он записывает в Order Fulfillment Fact `payment_authorized` с моментом решения и Change Notification. Маршрут: `AuthorizePayment → RecordPaymentAuthorization → RequestRestaurantOrder → …`.
- **Статус `PAID`.** В BFF у `OrderStatus` появляется значение `PAID` между `RECEIVED` и `CONFIRMED`, у `OrderMilestoneKind` — `PAID`, у `OrderEndReason` — `PAYMENT_DECLINED`. Как и `PREPARING`, `PAID` — клиентский статус, вычисляемый из факта. Состояние FSM Order и окно отмены не меняются.
- **UI.** Шаг «Оплачен» в карточке полного цикла флоу-борда (actor — платёж) и в прогрессе экрана заказа покупателя; локализованное сообщение для `PAYMENT_DECLINED`; MSW-моки и fixtures.

Не входит:

- явный no-op для `RestaurantOrderCancelled` в группе `orders` (отдельный change);
- удаление мёртвых сигналов `PaymentAuthorized` и `PaymentFailed`;
- versioning workflow и replay tests.

## Capabilities

### New Capabilities

- `order-flow-board`: карточка полного цикла заказа на флоу-борде.

### Modified Capabilities

- `order-fulfillment-orchestration`: старт только по `OrderPlaced`; ресторан запрашивается после оплаты; оплата фиксируется в Order.
- `order-use-cases`: Start Fulfillment и Record Payment Authorization; Place Order без старта workflow.
- `restaurant-order-lifecycle`: Restaurant Order больше не создаётся из `OrderPlaced`.
- `customer-graphql-contract`: `OrderStatus.PAID`, milestone `PAID`, `OrderEndReason.PAYMENT_DECLINED`.
- `order-tracking-ui`: шаг оплаты в прогрессе и сообщение об отказе оплаты.
- `kubernetes-test-environment`: проверка старта и оплаты в kind.
- `source-module-structure`: порт доступа consumer'а `restaurants`.

## Impact

- **`boundaries/orders`:**
  - слайс `order`: новые use cases `startfulfillment` и `recordpaymentauthorization`, факт `payment_authorized` в домене, правки `placeorder`, `fulfillment` (порты), `infrastructure/messaging`, `di`;
  - слайс `fulfillment`: шаг маршрута, Activity, адаптер `Client.Start` на `SignalWithStart`;
  - proto `orders/v1/service.proto` (BREAKING `reserved 3`) и `orders.proto` (факт `payment_authorized`), gRPC-адаптер.
- **`boundaries/restaurants`:** удаляются `slices/restaurantorder/infrastructure/integration/order-placed.ts`, его регистрация в `di/consumer.ts` и тесты consumer'а `OrderPlaced`; комментарии values `restaurants-consumer`.
- **`boundaries/bff`:** перегенерация клиента `orders`; `ordertracking` (статус, milestone, end reason), schema, presenter, тест checkout без `fulfillment_started`.
- **`boundaries/ui`:** `flow-board/order-flow.tsx`, `view-order`, i18n, generated types, моки.
- **`ops`:**
  - `ops/kind/manifests/messaging/dead-letter-topics.yaml` (комментарий: `orders.v1.order-events.dlq` теперь у группы `orders`);
  - `ops/kind/e2e/order-lifecycle`;
  - values `restaurants-consumer`.
- **Документация:**
  - ADR 5 orders, ADR 11;
  - `docs/domain-events.md`, `docs/use-cases.md`, `docs/kitchen.md`, `docs/fsm.md`;
  - README `orders`, `restaurants`, `bff`, `ui`, README слайсов.

В kind перед выкаткой worker'а нужно прервать незавершённые workflow, потому что код workflow меняется без versioning (`boundaries/orders/README.md`).

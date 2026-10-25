# Design

## Context

См. `proposal.md` и spec deltas `customer-graphql-contract`, `order-tracking-ui`. В `boundaries/bff/plugins/bff/src/schema.graphql` есть только `bffInfo`; Cosmo router plugin генерирует из схемы gRPC-контракт (`wgc router plugin generate`), а `BffService` встраивает `UnimplementedBffServiceServer`. UI генерирует типы из той же схемы (`codegen.ts`), держит provisional urql-клиент и MSW-обработчик `BffInfo`; правила UI — в [docs/ui.md](../../../docs/ui.md).

Источник данных заказа — `orders`: состояния `placed → confirmed → completed | cancelled`, Fulfillment Facts (`restaurant_rejected`, `preparation_started`, `ready_for_pickup`, `courier_assigned`, `picked_up`, `delivery_completed`, `delivery_failed`), `Cancellation { outcome: cancellation | failure, reason_code }`, окно отмены за портом `CancellationPolicy` и асинхронный Cancellation Request с исходами `requested | already_cancelled`. Задержка приготовления и ETA в `orders` не хранятся (`relaypreparationissue` только передаёт их workflow).

## Goals / Non-Goals

**Goals:**

- зафиксировать customer-схему, достаточную для просмотра ресторанов и меню, Place Order, отслеживания, истории и отмены заказа;
- дать UI работать на этой схеме через MSW с теми же documents и типами, что пойдут в production;
- сделать экран отслеживания заказа первым production-feature на контракте.

**Non-Goals:**

- resolvers BFF, подключение к internal gateway, user identity и аутентификация;
- корзина, checkout UI, оплата и статус возврата денег;
- ETA, задержка приготовления, данные и геопозиция курьера;
- subscriptions/SSE, браузерный E2E, выбор окончательного GraphQL client.

## Decisions

### 1. Схема описывает customer-представление, а не внутренние модели

Схема не повторяет gRPC-контракты bounded contexts. Публичный `OrderStatus` — линейный жизненный цикл для покупателя, который BFF вычисляет из состояния заказа и Fulfillment Facts:

| `OrderStatus` | Условие в `orders` |
| --- | --- |
| `RECEIVED` | `placed` |
| `CONFIRMED` | `confirmed`, нет `preparation_started` |
| `PREPARING` | есть `preparation_started`, нет `ready_for_pickup` |
| `READY_FOR_PICKUP` | есть `ready_for_pickup`, нет `picked_up` |
| `PICKED_UP` | есть `picked_up` |
| `DELIVERED` | `completed` |
| `CANCELLED` | `cancelled` с `outcome = cancellation` |
| `FAILED` | `cancelled` с `outcome = failure` |

Назначение курьера не входит в линейный статус: оно может произойти до или после готовности и отражается milestone `COURIER_ASSIGNED`. Для завершённого неуспешно заказа `endReason: OrderEndReason` (`CUSTOMER_CANCELLED`, `RESTAURANT_REJECTED`, `DELIVERY_FAILED`, `NOT_FULFILLED`) даёт причину; неизвестные `reason_code` BFF сводит к `NOT_FULFILLED`. UI владеет только отображением `OrderStatus → label/icon/progress`, как требует docs/ui.md.

Альтернатива — отдавать сырое состояние и facts и вычислять статус в UI. Отклонена: правило «какой fact главнее» — знание backend, а UI не должен знать FSM.

### 2. Набор операций

```graphql
type Query {
  bffInfo: BffInfo!
  restaurants(first: Int!, after: String): RestaurantConnection!
  restaurant(id: ID!): Restaurant
  order(id: ID!): Order
  customerOrders(first: Int!, after: String): OrderConnection!
}

type Mutation {
  placeOrder(input: PlaceOrderInput!): PlaceOrderPayload!
  requestOrderCancellation(input: RequestOrderCancellationInput!): RequestOrderCancellationPayload!
}
```

Ключевые типы: `Restaurant { id, name, address, acceptingOrders, menu }`, `MenuItem { id, name, description, price, available }`, `Order { id, status, endReason, restaurant, items, total, deliveryAddress, placedAt, milestones, cancellation }`, `OrderItem { menuItem { id, name }, quantity, unitPrice, lineTotal }`, `OrderMilestone { kind, occurredAt }`, `Money { amount, currency }` (`amount` — десятичная строка, как в proto `orders`). Пагинация — cursor connections (`edges { cursor node }`, `pageInfo { hasNextPage endCursor }`). Customer не передаётся аргументом: BFF берёт subject из аутентифицированного запроса.

`order(id)` возвращает `null` и для отсутствующего, и для чужого заказа, чтобы не раскрывать существование.

### 3. Бизнес-отказы — данные payload, а не GraphQL errors

`PlaceOrderPayload { order, problems: [PlaceOrderProblem!]! }`, где `PlaceOrderProblem { code, menuItemIds }` и `code ∈ ORDER_ITEM_UNAVAILABLE | ORDER_TOTAL_BELOW_MINIMUM | ORDER_TOTAL_ABOVE_MAXIMUM | RESTAURANT_NOT_ACCEPTING_ORDERS` — отражение reason codes `OrderItemsOrderable` и `RestaurantAcceptingOrders`. `RequestOrderCancellationPayload { outcome, problem }`: `outcome ∈ REQUESTED | ALREADY_CANCELLED`, `problem.code ∈ CANCELLATION_WINDOW_NOT_OPEN | CANCELLATION_WINDOW_EXPIRED | ORDER_ALREADY_FINAL`. GraphQL errors остаются для transport, authentication и «не реализовано».

Unions (`Success | Problem`) не используются, пока поддержка в генераторе Cosmo router plugin не подтверждена; payload с полем `problems` работает в любом случае. По той же причине нет custom scalars: моменты времени — `String` в RFC 3339 с описанием в схеме.

### 4. Идемпотентность mutations

`PlaceOrderInput.idempotencyKey` и `RequestOrderCancellationInput.requestId` — обязательные строки, которые UI создаёт один раз на попытку (`crypto.randomUUID()`) и переиспользует при ручном retry. Это совпадает с idempotency `placeorder` и `RequestID` Cancellation Request в `orders`.

### 5. Окно отмены как данные заказа

`Order.cancellation: OrderCancellationAvailability` (`availableUntil: String!`) присутствует, только если отмена сейчас допустима по окну. UI показывает действие и время, до которого оно доступно, но окончательное решение остаётся за backend: ответ `CANCELLATION_WINDOW_EXPIRED` показывается как обычный бизнес-отказ. Сейчас `GetOrder` окно не возвращает — BFF сможет заполнить поле только после того, как `orders` его опубликует; это отдельное изменение `orders`.

`REQUESTED` означает принятый асинхронный запрос: UI показывает «запрос отправлен» и ждёт `CANCELLED` или прежний ход заказа через polling.

### 6. BFF: схема и сгенерированный код, без resolvers

Plugin регенерируется (`npm run generate` в `boundaries/bff`), `BffService` продолжает встраивать `UnimplementedBffServiceServer`, поэтому новые поля возвращают GraphQL error с gRPC `Unimplemented`. `npm run check` BFF (generate, test, build, compose) проходит. Реализация resolvers — по одной операции в следующих changes по мере готовности bounded contexts и user identity.

### 7. MSW: сценарии заказа и проверка моков схемой

Обработчики всех новых операций живут в `src/shared/graphql/mocks/`, данные — в typed fixtures, совместимых с generated types. Сценарии заказа адресуются стабильными id:

| id | Что показывает |
| --- | --- |
| `order-received` | только что оформлен, отмена доступна |
| `order-preparing` | готовится, курьер назначен |
| `order-picked-up` | в пути |
| `order-delivered` | доставлен |
| `order-cancelled` | отменён покупателем |
| `order-restaurant-rejected` | ресторан отказал |
| `order-delivery-failed` | доставка не удалась |
| `order-live` | продвигается на один статус за запрос до `DELIVERED` |

Mutations моков реализуют idempotency: повтор с тем же ключом возвращает тот же результат. Unit-тест исполняет каждый GraphQL document UI против `buildSchema(schema.graphql)` с ответами моков в качестве resolvers и падает при несовпадении формы — так моки не расходятся со схемой.

### 8. Экран `view-order`

Route `/orders/:orderId` в `features/view-order/` с colocated `order.graphql`, mapping, компонентами и тестами. Состав экрана: заголовок со статусом, прогресс по шагам `RECEIVED → CONFIRMED → PREPARING → READY_FOR_PICKUP → PICKED_UP → DELIVERED` (отметка «курьер назначен» внутри шагов приготовления), ресторан, состав и итог, адрес, история milestones с локальным временем, блок отмены. Неуспешное завершение заменяет прогресс сообщением по `endReason` без обещаний о возврате денег: статуса оплаты в схеме нет.

Обновление — bounded polling: `network-only` запрос каждые 10 с, только при видимом документе (`visibilitychange`), остановка на `DELIVERED`, `CANCELLED`, `FAILED`; при transport error интервал удваивается до 60 с и доступен ручной retry. Состояния загрузки — layout-stable skeleton; `null` — «заказ не найден»; transport error и `Unimplemented` — generic recovery UI без технических деталей. Изменение статуса объявляется через `aria-live="polite"`.

## Risks / Trade-offs

- [Схема опережает backend и может потребовать правок] → поля, требующие новых данных (`cancellation`, `MenuItem` names в `OrderItem`), явно перечислены как зависимости; до первого resolver схема меняется без deprecation.
- [Генератор Cosmo plugin может не принять часть конструкций] → задача 1.2 проверяет генерацию до работы над UI; схема не использует unions и custom scalars.
- [Имена позиций в заказе требуют композиции BFF из `orders` и `restaurants`] → это ответственность BFF по ADR о BFF; если меню недоступно, поле останется обязательным, а resolver вернёт ошибку — UI покажет recovery UI.
- [Моки могут выдавать бизнес-правила за реальные] → сценарии заказа — заранее заданные снимки; `placeOrder` моков считает суммы и окно отмены упрощённо, только чтобы вернуть заказ правильной формы. README UI называет моки development-only.
- [Polling нагружает BFF] → интервал и остановка ограничены экраном и финальными статусами; subscriptions остаются открытым решением docs/ui.md.

## Migration Plan

1. Расширить схему, регенерировать plugin, проверить `npm run check` BFF.
2. Перегенерировать UI types, добавить fixtures, обработчики и тест соответствия схеме.
3. Добавить feature `view-order` и route; preview на `/` не трогать.
4. Обновить README BFF и UI, docs/ui.md.
5. Rollback: удалить route и feature, вернуть схему к `bffInfo` и регенерировать оба артефакта.

# customer-graphql-contract Specification

## Purpose
Определяет публичную customer GraphQL-схему BFF и MSW-моки, которые обслуживают её в development и тестах: операции, customer-представление статуса заказа, бизнес-отказы mutations и идемпотентность.

## Requirements

### Requirement: Публичная customer-схема BFF
**ID:** FR-450

Публичная GraphQL-схема BFF SHALL предоставлять customer-операции: `restaurants` и `restaurant(id)` с меню, `order(id)`, `customerOrders` с cursor-пагинацией, mutations `placeOrder` и `requestOrderCancellation`. Операции MUST NOT принимать идентификатор покупателя аргументом: subject определяется BFF по аутентифицированному запросу.

#### Scenario: Генерация клиентских типов
**ID:** AC-486

- **WHEN** UI запускает `npm run codegen` против `boundaries/bff/plugins/bff/src/schema.graphql`
- **THEN** для каждой customer-операции генерируются typed documents без ошибок валидации

#### Scenario: Заказ недоступен покупателю
**ID:** AC-487

- **WHEN** `order(id)` запрашивается для отсутствующего заказа или заказа другого покупателя
- **THEN** поле `order` равно `null`, и ответ не различает эти случаи

### Requirement: Customer-представление статуса заказа
**ID:** FR-451

`Order.status` SHALL принимать значения публичного enum `OrderStatus` (`RECEIVED`, `CONFIRMED`, `PREPARING`, `READY_FOR_PICKUP`, `PICKED_UP`, `DELIVERED`, `CANCELLED`, `FAILED`), а неуспешно завершённый заказ SHALL содержать `endReason` из `OrderEndReason`. Схема MUST NOT раскрывать имена состояний FSM, Fulfillment Facts, Temporal workflow или gRPC-контрактов bounded contexts. Назначение курьера SHALL отражаться milestone `COURIER_ASSIGNED`, а не статусом.

#### Scenario: Отказ ресторана
**ID:** AC-488

- **WHEN** заказ отменён по факту `restaurant_rejected`
- **THEN** `status = FAILED` и `endReason = RESTAURANT_REJECTED`

#### Scenario: Курьер назначен до готовности
**ID:** AC-489

- **WHEN** заказ готовится и курьер уже назначен
- **THEN** `status = PREPARING`, а `milestones` содержит `COURIER_ASSIGNED` с моментом назначения

### Requirement: Бизнес-отказы mutations в payload
**ID:** FR-452

`placeOrder` SHALL возвращать бизнес-отказы списком `problems` с кодами `ORDER_ITEM_UNAVAILABLE`, `ORDER_TOTAL_BELOW_MINIMUM`, `ORDER_TOTAL_ABOVE_MAXIMUM`, `RESTAURANT_NOT_ACCEPTING_ORDERS` и идентификаторами затронутых позиций. GraphQL errors MUST использоваться только для transport, authentication и нереализованных операций.

#### Scenario: Позиция закончилась
**ID:** AC-490

- **WHEN** `placeOrder` содержит недоступную позицию меню
- **THEN** ответ без GraphQL errors содержит `order = null` и problem `ORDER_ITEM_UNAVAILABLE` с id этой позиции

### Requirement: Исход Cancellation Request в payload
**ID:** FR-461

`requestOrderCancellation` SHALL возвращать `outcome` (`REQUESTED`, `ALREADY_CANCELLED`) либо `problem` с кодом `CANCELLATION_WINDOW_NOT_OPEN`, `CANCELLATION_WINDOW_EXPIRED` или `ORDER_ALREADY_FINAL`, без GraphQL errors.

#### Scenario: Окно отмены закрыто
**ID:** AC-503

- **WHEN** `requestOrderCancellation` вызывается после закрытия окна отмены
- **THEN** ответ без GraphQL errors содержит problem `CANCELLATION_WINDOW_EXPIRED`

### Requirement: Идемпотентные customer-mutations
**ID:** FR-453

`PlaceOrderInput.idempotencyKey` и `RequestOrderCancellationInput.requestId` SHALL быть обязательными. Повтор mutation с тем же ключом MUST возвращать результат первой попытки и не создавать второй заказ или второй Cancellation Request.

#### Scenario: Повтор Place Order
**ID:** AC-491

- **WHEN** `placeOrder` повторяется с тем же `idempotencyKey`
- **THEN** ответ содержит тот же `order.id`, что и первая попытка

### Requirement: Схема опережает resolvers
**ID:** FR-454

BFF plugin SHALL генерироваться и собираться из расширенной схемы, а нереализованные customer-поля SHALL возвращать GraphQL error, не нарушая работу `bffInfo`.

#### Scenario: Запрос нереализованной операции
**ID:** AC-492

- **WHEN** клиент запрашивает `order(id)` у BFF до реализации resolver
- **THEN** ответ содержит GraphQL error, а запрос `bffInfo` продолжает возвращать данные

### Requirement: MSW-моки соответствуют схеме
**ID:** FR-455

UI SHALL иметь MSW-обработчики всех customer-операций, включённые только в development (`VITE_ENABLE_MOCKS=true`) и тестах, со сценариями заказа `order-received`, `order-preparing`, `order-picked-up`, `order-delivered`, `order-cancelled`, `order-restaurant-rejected`, `order-delivery-failed` и `order-live`. Ответы моков MUST проходить валидацию против той же схемы, что и production documents, а mutations моков MUST соблюдать идемпотентность по ключу.

#### Scenario: Мок расходится со схемой
**ID:** AC-493

- **WHEN** fixture мока содержит поле или значение enum, которого нет в схеме
- **THEN** тест соответствия моков схеме падает

#### Scenario: Заказ продвигается между запросами
**ID:** AC-494

- **WHEN** UI повторно запрашивает `order(id: "order-live")`
- **THEN** каждый ответ продвигает `status` на один шаг до `DELIVERED`

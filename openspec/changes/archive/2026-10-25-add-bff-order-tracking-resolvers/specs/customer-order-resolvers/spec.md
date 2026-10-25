# Spec Delta

## ADDED Requirements

### Requirement: order(id) читает заказ от имени покупателя
**ID:** FR-490

Resolver `order(id)` SHALL проверять access token покупателя и вызывать `orders.GetOrder` с user assertion этого покупателя; при `NOT_FOUND` или `PERMISSION_DENIED` он SHALL возвращать `null`. Отсутствующий или невалидный access token MUST давать ошибку аутентификации без вызова `orders`.

#### Scenario: Чужой заказ
**ID:** AC-555

- **WHEN** покупатель запрашивает `order(id)` заказа другого покупателя, и `orders` отвечает `PERMISSION_DENIED`
- **THEN** поле `order` равно `null`, и ответ не отличается от отсутствующего заказа

#### Scenario: Запрос без access token
**ID:** AC-556

- **WHEN** запрос `order(id)` пришёл без заголовка `Authorization`
- **THEN** ответ содержит ошибку аутентификации, а `orders` не вызывается

### Requirement: Customer-представление строится из данных orders и restaurants
**ID:** FR-491

Resolver SHALL строить `status`, `endReason`, `milestones`, позиции с названиями из меню ресторана и `lineTotal`, а также `cancellation.availableUntil` по правилам design; суммы MUST считаться десятичной арифметикой без float.

#### Scenario: Готовящийся заказ с назначенным курьером
**ID:** AC-557

- **WHEN** `orders` возвращает `confirmed` с фактами `preparation_started` и `courier_assigned`
- **THEN** `status = PREPARING`, а `milestones` содержат `PLACED`, `PREPARATION_STARTED` и `COURIER_ASSIGNED` в порядке времени

#### Scenario: Окно отмены открыто
**ID:** AC-558

- **WHEN** заказ `placed`, и часы BFF находятся между `opens_at` и `closes_at` окна
- **THEN** `cancellation.availableUntil` равно `closes_at`

#### Scenario: Сумма позиции без float
**ID:** AC-559

- **WHEN** позиция стоит `0.10` и заказана 3 раза
- **THEN** `lineTotal.amount = "0.30"`

### Requirement: requestOrderCancellation переводит исход orders
**ID:** FR-492

Resolver `requestOrderCancellation` SHALL вызывать `orders.RequestOrderCancellation` от имени покупателя с его `requestId` и переводить исход в `outcome`, а отказы `CANCELLATION_WINDOW_NOT_OPEN`, `CANCELLATION_WINDOW_EXPIRED` и `ORDER_STATE_CONFLICT` — в `problem` (`ORDER_STATE_CONFLICT` → `ORDER_ALREADY_FINAL`) без GraphQL errors.

#### Scenario: Окно закрылось
**ID:** AC-560

- **WHEN** `orders` отвечает `FAILED_PRECONDITION` с `ErrorInfo.reason = CANCELLATION_WINDOW_EXPIRED`
- **THEN** ответ содержит `problem.code = CANCELLATION_WINDOW_EXPIRED` и не содержит GraphQL errors

# Spec Delta

## MODIFIED Requirements

### Requirement: Публичная customer-схема BFF
**ID:** FR-450

Публичная GraphQL-схема BFF SHALL предоставлять customer-операции: `restaurants` и `restaurant(id)` с меню, `order(id)`, `customerOrders` с cursor-пагинацией сводок заказов (`OrderSummary`), mutations `placeOrder` и `requestOrderCancellation`. Операции MUST NOT принимать идентификатор покупателя аргументом: subject определяется BFF по аутентифицированному запросу.

#### Scenario: Генерация клиентских типов
**ID:** AC-486

- **WHEN** UI запускает `npm run codegen` против `boundaries/bff/plugins/bff/src/schema.graphql`
- **THEN** для каждой customer-операции генерируются typed documents без ошибок валидации

#### Scenario: Заказ недоступен покупателю
**ID:** AC-487

- **WHEN** `order(id)` запрашивается для отсутствующего заказа или заказа другого покупателя
- **THEN** поле `order` равно `null`, и ответ не различает эти случаи

## ADDED Requirements

### Requirement: Ресторан вне каталога — сводка
**ID:** FR-670

`Restaurant` SHALL возвращаться только запросами каталога `restaurants` и `restaurant(id)` и всегда содержать `waitTime`; `null` его полей MUST означать отсутствие данных. Ресторан в заказе, на кухне, в `myRestaurants` и в payload открытия SHALL быть `RestaurantSummary { id name acceptingOrders }`.

#### Scenario: Ресторан в заказе
**ID:** AC-891

- **WHEN** клиент запрашивает `order(id)` с выбором `restaurant { menu }`
- **THEN** документ не проходит валидацию: у `RestaurantSummary` нет поля `menu`

#### Scenario: Ожидание в каталоге
**ID:** AC-892

- **WHEN** `restaurants` отвечает рестораном
- **THEN** `waitTime` ресторана не `null`

### Requirement: История заказов — сводки
**ID:** FR-671

Узел `customerOrders` SHALL быть `OrderSummary { id status endReason placedAt total priority restaurant }`. У `Order` поля `courier` и `tracking` SHALL быть `null` только по состоянию доставки, а не по месту чтения.

#### Scenario: История заказов
**ID:** AC-893

- **WHEN** покупатель запрашивает `customerOrders(first: 1)` и у него есть заказ
- **THEN** узел — `OrderSummary` с `status`, `total` и `restaurant { name }`, без полей курьера и трекинга

# Spec Delta

## ADDED Requirements

### Requirement: Fulfillment-процессы orders в тестовом окружении
**ID:** FR-584

Тестовое окружение SHALL разворачивать `orders-worker` и `orders-forwarder` из chart `orders` с той же сборкой образа, что и API `orders`, с readiness и диагностикой, как у остальных процессов.

#### Scenario: Заказ начинает исполняться
**ID:** AC-739

- **WHEN** покупатель оформил заказ через BFF
- **THEN** workflow авторизует оплату и запрашивает ресторан, а `restaurants-consumer` получает `OrderPlaced`

### Requirement: Путь заказа проверяется насквозь
**ID:** FR-585

`make kind-test` SHALL выполнять Chainsaw-тест полного пути заказа: подготовка ресторана, меню и курьера, оформление заказа покупателем через BFF, действия кухни и курьера через internal gateway и итоговое состояние заказа в BFF. Действия кухни и курьера MUST выполняться fixture `e2e-driver`, которому только в kind-values разрешено передавать user assertion в `restaurants` и `delivery`; operator MUST выдаваться kind-only bootstrap SpiceDB.

#### Scenario: Заказ доставлен
**ID:** AC-740

- **WHEN** тест проходит путь от `placeOrder` до Confirm Delivery
- **THEN** `order(id)` в BFF возвращает `DELIVERED` с milestones `COURIER_ASSIGNED`, `PICKED_UP` и `DELIVERED`, а оплата списана

#### Scenario: Делегат только в kind
**ID:** AC-741

- **WHEN** проверяются chart-default values `restaurants` и `delivery`
- **THEN** `e2e-driver` не входит ни в allowlist, ни в `userDelegates`

# Spec Delta

## ADDED Requirements

### Requirement: Запланированный заказ в customer-схеме
**ID:** FR-685

Публичная схема SHALL содержать `Restaurant.scheduledSlots: [DeliveryWindow!]!` (`start`, `end`, `available`), `PlaceOrderInput.deliveryWindowStart`, `Order.deliveryWindow` и значение `OrderStatus.SCHEDULED` для подтверждённого запланированного заказа без факта Preparation Started. Схема MUST NOT раскрывать план workflow (`start_at`, `courier_request_at`).

#### Scenario: Заказ ждёт старта
**ID:** AC-927

- **WHEN** ресторан принял заказ на 19:00, а приготовление не начато
- **THEN** `status = SCHEDULED`, `deliveryWindow` — 19:00–19:15

### Requirement: Отказы резервации окна в payload
**ID:** FR-690

`placeOrder` SHALL возвращать отказы резервации в payload как problems `SLOT_UNAVAILABLE` с `alternativeWindows` и `SCHEDULED_ORDERS_UNAVAILABLE`, а не как GraphQL errors.

#### Scenario: Окно заняли
**ID:** AC-928

- **WHEN** `placeOrder` получает отказ резервации `SLOT_UNAVAILABLE`
- **THEN** payload содержит problem `SLOT_UNAVAILABLE` с альтернативными окнами

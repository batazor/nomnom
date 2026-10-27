# Spec Delta

## ADDED Requirements

### Requirement: Place Order принимает приоритет и фиксирует доплату
**ID:** FR-650

Place Order SHALL требовать `priority` (`STANDARD` или `PREMIUM`); отсутствующее или неизвестное значение MUST отклоняться как некорректный заказ. Для `PREMIUM` доплата SHALL браться из snapshot ресторана, фиксироваться в Order и входить в `total`. Ресторан без доплаты MUST давать отказ `PREMIUM_UNAVAILABLE` (`FAILED_PRECONDITION`) без сохранения Order. Order SHALL хранить `priority`, и Activity `RequestRestaurantOrder` SHALL передавать его в `ReceiveOrderRequest`.

#### Scenario: Премиум-заказ
**ID:** AC-854

- **GIVEN** доплата ресторана 99.00 RUB, позиции на 500.00 RUB
- **WHEN** Customer оформляет заказ с `priority = PREMIUM`
- **THEN** Order хранит `priority = PREMIUM`, доплату 99.00 RUB и `total` 599.00 RUB

#### Scenario: Ресторан без премиума
**ID:** AC-855

- **WHEN** Customer оформляет заказ с `priority = PREMIUM` в ресторане без доплаты
- **THEN** оформление отклоняется `PREMIUM_UNAVAILABLE`, Order не сохранён, Outbox не пополнен

#### Scenario: Доплата изменилась после оформления
**ID:** AC-856

- **WHEN** ресторан меняет доплату после оформления премиум-заказа
- **THEN** доплата и `total` этого Order не меняются

#### Scenario: Приоритет доходит до кухни
**ID:** AC-857

- **WHEN** workflow выполняет `RequestRestaurantOrder` премиум-заказа
- **THEN** `ReceiveOrderRequest` получает `priority = PREMIUM`

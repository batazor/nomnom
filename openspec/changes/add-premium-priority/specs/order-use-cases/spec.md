# Spec Delta

## ADDED Requirements

### Requirement: Place Order принимает приоритет и фиксирует доплату
**ID:** FR-650

Place Order SHALL принимать `priority`: не задан — `STANDARD`, неизвестный — некорректный заказ. Доплата `PREMIUM` SHALL браться из snapshot ресторана, фиксироваться в Order и входить в `total`; ресторан без доплаты MUST давать `PREMIUM_UNAVAILABLE` (`FAILED_PRECONDITION`) без сохранения Order. Activity `RequestRestaurantOrder` SHALL передавать `priority` Order в `ReceiveOrderRequest`.

#### Scenario: Премиум-заказ
**ID:** AC-854

- **GIVEN** доплата ресторана 99.00 RUB, позиции на 500.00 RUB
- **WHEN** Customer оформляет заказ с `priority = PREMIUM`
- **THEN** Order хранит `priority = PREMIUM`, доплату 99.00 RUB и `total` 599.00 RUB

#### Scenario: Заказ без приоритета
**ID:** AC-870

- **WHEN** Customer оформляет заказ, не указав `priority`
- **THEN** Order сохранён с `priority = STANDARD` без доплаты

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

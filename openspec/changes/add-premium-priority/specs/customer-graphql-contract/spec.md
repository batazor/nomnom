# Spec Delta

## ADDED Requirements

### Requirement: Премиум-приоритет в customer-схеме
**ID:** FR-652

`Restaurant` SHALL иметь `premiumFee: Money` (null — опции нет), `PlaceOrderInput` — необязательный `priority: OrderPriority` (`STANDARD`, `PREMIUM`; не задан — `STANDARD`), `Order` — `priority: OrderPriority!` и `premiumFee: Money` (null для `STANDARD`). Отказ `PREMIUM_UNAVAILABLE` SHALL возвращаться problem в payload `placeOrder`. MSW-моки SHALL покрывать ресторан с доплатой и без неё.

#### Scenario: Ресторан без премиума
**ID:** AC-863

- **WHEN** `placeOrder` с `priority = PREMIUM` вызван для ресторана без доплаты
- **THEN** payload содержит problem `PREMIUM_UNAVAILABLE`, заказ не создан

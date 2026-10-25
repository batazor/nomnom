# Spec Delta

## MODIFIED Requirements

### Requirement: Confirm Order отражает Order Acceptance
**ID:** FR-105

Confirm Order SHALL переводить `Order` из `placed` в `confirmed` по событию Order Acceptance ресторана, в той же транзакции записывать Fulfillment Fact `restaurant_accepted` с моментом события и после commit передавать бизнес-факт Order Fulfillment Workflow вместе с исходным `event_id`.

#### Scenario: Ресторан принял заказ
**ID:** AC-108

- **WHEN** `Order` в состоянии `placed` получает событие Order Acceptance
- **THEN** `Order` переходит в `confirmed`, а workflow получает факт принятия с исходным `event_id`

#### Scenario: Момент подтверждения сохраняется
**ID:** AC-563

- **WHEN** событие Order Acceptance с `OccurredAt = 12:03` подтверждает `Order`
- **THEN** `GetOrder` возвращает факт `restaurant_accepted` с моментом `12:03`

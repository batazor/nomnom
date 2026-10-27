# Spec Delta

## MODIFIED Requirements

### Requirement: Customer-представление статуса заказа
**ID:** FR-451

`Order.status` SHALL принимать значения публичного enum `OrderStatus` (`RECEIVED`, `PAID`, `CONFIRMED`, `PREPARING`, `READY_FOR_PICKUP`, `PICKED_UP`, `DELIVERED`, `CANCELLED`, `FAILED`), а неуспешно завершённый заказ SHALL содержать `endReason` из `OrderEndReason`. Схема MUST NOT раскрывать имена состояний FSM, Fulfillment Facts, Temporal workflow или gRPC-контрактов bounded contexts. Назначение курьера SHALL отражаться milestone `COURIER_ASSIGNED`, а не статусом. `PAID` SHALL означать, что оплата авторизована, а ресторан ещё не принял заказ; момент оплаты SHALL отражаться milestone `PAID`. Отказ Authorization или Capture SHALL давать `endReason = PAYMENT_DECLINED`.

#### Scenario: Отказ ресторана
**ID:** AC-488

- **WHEN** заказ отменён по факту `restaurant_rejected`
- **THEN** `status = FAILED` и `endReason = RESTAURANT_REJECTED`

#### Scenario: Курьер назначен до готовности
**ID:** AC-489

- **WHEN** заказ готовится и курьер уже назначен
- **THEN** `status = PREPARING`, а `milestones` содержит `COURIER_ASSIGNED` с моментом назначения

#### Scenario: Оплата авторизована
**ID:** AC-831

- **WHEN** у заказа в `placed` записан факт оплаты, а ресторан его ещё не принял
- **THEN** `status = PAID`, а `milestones` содержит `PAID` с моментом оплаты

#### Scenario: Оплата отклонена
**ID:** AC-832

- **WHEN** заказ завершён с reason `payment_declined` или `payment_capture_declined`
- **THEN** `status = FAILED` и `endReason = PAYMENT_DECLINED`

# Spec Delta

## ADDED Requirements

### Requirement: Get Kitchen Queue доступен по gRPC
**ID:** FR-583

`restaurants` SHALL публиковать `GetKitchenQueue(restaurant_id)` по gRPC: заказы очереди кухни в рекомендованном порядке с `restaurant_order_id`, `order_id`, состоянием и версией. RPC MUST требовать `restaurant#operate_kitchen` и проверенной identity.

#### Scenario: Сотрудник видит новый заказ
**ID:** AC-737

- **WHEN** ресторан получил заказ, а сотрудник с `operate_kitchen` вызывает `GetKitchenQueue`
- **THEN** ответ содержит заказ с его `restaurant_order_id` и `order_id`

#### Scenario: Посторонний
**ID:** AC-738

- **WHEN** пользователь без `operate_kitchen` вызывает `GetKitchenQueue`
- **THEN** ответ `PERMISSION_DENIED`

# Spec Delta

## ADDED Requirements

### Requirement: Get Kitchen Queue доступен по gRPC
**ID:** FR-583

`restaurants` SHALL публиковать `GetKitchenQueue(restaurant_id)` по gRPC: заказы очереди кухни в рекомендованном порядке с `restaurant_order_id`, `order_id`, состоянием и версией. RPC MUST требовать `restaurant#operate_kitchen` и проверенной identity.

#### Scenario: Сотрудник видит новый заказ
**ID:** AC-737

- **WHEN** ресторан принял заказ, а сотрудник с `operate_kitchen` вызывает `GetKitchenQueue`
- **THEN** ответ содержит заказ с его `restaurant_order_id` и `order_id`

#### Scenario: Посторонний
**ID:** AC-738

- **WHEN** пользователь без `operate_kitchen` вызывает `GetKitchenQueue`
- **THEN** ответ `PERMISSION_DENIED`

### Requirement: Входящие заказы доступны кухне
**ID:** FR-586

`restaurants` SHALL предоставлять query List Incoming Orders и публиковать его по gRPC как `ListIncomingOrders(restaurant_id)`: заказы ресторана в состоянии `RECEIVED`, ожидающие Accept или Reject, по возрастанию времени получения, с `restaurant_order_id`, `order_id`, позициями, версией и временем получения. Query MUST требовать `restaurant#operate_kitchen`.

#### Scenario: Новый заказ ждёт приёма
**ID:** AC-742

- **WHEN** ресторан получил заказ, а сотрудник с `operate_kitchen` вызывает `ListIncomingOrders`
- **THEN** ответ содержит заказ в `RECEIVED` с его `restaurant_order_id` и `order_id`, а принятый заказ в ответе отсутствует

#### Scenario: Посторонний
**ID:** AC-743

- **WHEN** пользователь без `operate_kitchen` вызывает `ListIncomingOrders`
- **THEN** ответ `PERMISSION_DENIED`

# kitchen-board Specification

## Purpose
Определяет read models kitchen board `restaurants`: Kitchen Queue, заказы в работе кухни и заказы, ожидающие передачи курьеру, для авторизованных сотрудников ресторана.

## Requirements

### Requirement: Read models кухни доступны только сотрудникам ресторана
**ID:** FR-240

Get Kitchen Queue, List Active Preparations и List Ready Orders SHALL требовать permission subject над рестораном, выводимую из relation `staff`, и MUST возвращать только заказы этого ресторана без изменения состояния.

#### Scenario: Чужой ресторан
**ID:** AC-280

- **WHEN** subject без права над рестораном запрашивает его Kitchen Queue
- **THEN** запрос отклоняется как permission denied

### Requirement: Get Kitchen Queue возвращает заказы в рекомендованном порядке
**ID:** FR-241

Get Kitchen Queue SHALL возвращать заказы ресторана в `QUEUED` с `accepted_at`, `queued_at` и `expected_ready_at`. Порядок MUST определяться заданной политикой очереди, а не самим запросом; Kitchen Queue MUST NOT быть отдельным aggregate.

#### Scenario: Очередь по политике
**ID:** AC-281

- **WHEN** в очереди несколько принятых заказов
- **THEN** возвращаются только заказы в `QUEUED` в порядке, заданном политикой очереди

### Requirement: List Active Preparations показывает заказы в работе
**ID:** FR-242

List Active Preparations SHALL возвращать заказы ресторана в `PREPARING` и `ASSEMBLED` с State, `preparation_started_at` и текущим `expected_ready_at`, включая перенесённое Preparation Delay.

#### Scenario: Готовящиеся и собранные заказы
**ID:** AC-282

- **WHEN** у ресторана есть заказы в очереди, в Preparation, собранные и готовые
- **THEN** возвращаются только готовящиеся и собранные заказы с актуальным `expected_ready_at`

### Requirement: List Ready Orders показывает заказы, готовые к Pickup
**ID:** FR-243

List Ready Orders SHALL возвращать заказы ресторана в `READY` с `ready_at`. Информационные данные курьера MUST NOT влиять на State заказа и не входят в результат до появления projection событий `delivery`.

#### Scenario: Готовые заказы
**ID:** AC-283

- **WHEN** у ресторана есть готовые и готовящиеся заказы
- **THEN** возвращаются только заказы в `READY` с `ready_at`

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

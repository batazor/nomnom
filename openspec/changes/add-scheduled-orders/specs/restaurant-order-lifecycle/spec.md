# Spec Delta

## ADDED Requirements

### Requirement: Restaurant Order хранит окно запланированного заказа
**ID:** FR-683

Receive Order Request SHALL принимать необязательный блок `schedule` с `window_start`, `window_end` и `planned_ready_at` и сохранять его в Restaurant Order как данные, а не как State. Неполный блок или `planned_ready_at` не раньше `window_start` MUST отклоняться `INVALID_ARGUMENT`.

#### Scenario: Запланированный заказ на кухне
**ID:** AC-921

- **WHEN** `ReceiveOrderRequest` приходит с окном 19:00–19:15 и `planned_ready_at` 18:40
- **THEN** Restaurant Order создан в `RECEIVED` с этим окном и сроком

## MODIFIED Requirements

### Requirement: Accept Order принимает заказ и ставит его в Kitchen Queue
**ID:** FR-222

Accept Order SHALL принимать только заказ в `RECEIVED`, если ресторан открыт, удовлетворяет `RestaurantAcceptingOrders` и все позиции существуют в меню и удовлетворяют `MenuItemAvailable` на момент принятия. В той же Unit of Work заказ без окна MUST перейти в `QUEUED`, получить `accepted_at`, `queued_at`, `expected_ready_at`; запланированный заказ MUST перейти в `ACCEPTED` с `accepted_at` и `expected_ready_at = planned_ready_at` без `queued_at`, а срок от кухни для него MUST отклоняться `INVALID_ARGUMENT`. В Outbox MUST быть записан только `RestaurantOrderAccepted`.

#### Scenario: Принятие открытым рестораном
**ID:** AC-246

- **WHEN** сотрудник принимает полученный заказ открытого ресторана с доступными позициями
- **THEN** заказ в `QUEUED`, Preparation не начата, в Outbox записан ровно один `RestaurantOrderAccepted`

#### Scenario: Ресторан закрыт или позиция недоступна
**ID:** AC-247

- **WHEN** ресторан закрыт, не принимает заказы по расписанию или capacity, либо позиция отсутствует или недоступна
- **THEN** операция отклоняется как failed precondition с перечнем причин, заказ остаётся в `RECEIVED`, Outbox не меняется

#### Scenario: Повтор принятия
**ID:** AC-248

- **WHEN** принимается заказ, уже находящийся в Kitchen Queue
- **THEN** операция отклоняется как invalid transition без нового бизнес-факта

#### Scenario: Принятие запланированного заказа
**ID:** AC-922

- **WHEN** сотрудник принимает запланированный заказ с `planned_ready_at` 18:40
- **THEN** заказ в `ACCEPTED`, не в Kitchen Queue, `RestaurantOrderAccepted` несёт `expected_ready_at` 18:40

#### Scenario: Свой срок для запланированного заказа
**ID:** AC-923

- **WHEN** сотрудник принимает запланированный заказ с `expected_ready_at`
- **THEN** операция отклоняется `INVALID_ARGUMENT`, заказ остаётся в `RECEIVED`

### Requirement: Queue Restaurant Order ставит принятый заказ в очередь
**ID:** FR-224

Queue Restaurant Order SHALL переводить заказ из `ACCEPTED` в `QUEUED` с `queued_at` как локальный факт без публикации и SHALL быть доступен по gRPC как `QueueRestaurantOrder(restaurant_order_id)` только `service` identity `orders`. `expected_ready_at` MUST NOT меняться. Повтор для заказа, уже находящегося в `QUEUED`, SHALL быть успешным без изменения состояния.

#### Scenario: Отложенная постановка в очередь
**ID:** AC-251

- **WHEN** система ставит в очередь принятый заказ
- **THEN** заказ в `QUEUED`, `queued_at` установлен, Outbox не меняется

#### Scenario: Повторная постановка
**ID:** AC-252

- **WHEN** система ставит в очередь заказ, уже находящийся в `QUEUED`
- **THEN** операция успешна без изменения версии заказа

#### Scenario: Постановка от сотрудника
**ID:** AC-924

- **WHEN** пользователь с `operate_kitchen` вызывает `QueueRestaurantOrder`
- **THEN** ответ `PERMISSION_DENIED`, заказ остаётся в `ACCEPTED`

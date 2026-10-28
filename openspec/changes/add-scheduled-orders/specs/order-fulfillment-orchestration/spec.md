# Spec Delta

## ADDED Requirements

### Requirement: Резервация окна входит в Saga
**ID:** FR-682

Для запланированного заказа workflow SHALL после `RecordPaymentAuthorization` выполнить `ConfirmSlotReservation`, а при отказе оплаты, отмене или неудаче до Pickup — компенсацию `ReleaseSlotReservation` с ключом `order_id + release_slot`. `SLOT_UNAVAILABLE` при подтверждении SHALL вести к отмене Authorization и завершению `Order` кодом `slot_unavailable` без запроса ресторана. Ветка запланированного заказа MUST вводиться под Temporal versioning.

#### Scenario: Оплата отклонена
**ID:** AC-919

- **WHEN** `payments` отклоняет Authorization запланированного заказа
- **THEN** workflow освобождает резервацию и завершает `Order` с reason `payment_declined`

#### Scenario: Отмена в stage scheduled
**ID:** AC-920

- **WHEN** клиент отменяет принятый запланированный заказ до старта
- **THEN** workflow отменяет Restaurant Order, освобождает резервацию, отменяет Authorization и завершает `Order` с кодом `customer_requested`

## MODIFIED Requirements

### Requirement: Маршрут workflow выполняется шагами ADR 5
**ID:** FR-415

Order Fulfillment Workflow SHALL выполнять AuthorizePayment, затем RequestRestaurantOrder, после `RestaurantOrderAccepted` — CreateDelivery, после `DeliveryPickedUp` — CapturePayment, после `DeliveryCompleted` — CompleteOrder. Для запланированного заказа между Authorization и RequestRestaurantOrder SHALL выполняться `ConfirmSlotReservation` и `PlanSchedule`, RequestRestaurantOrder SHALL передавать окно и `planned_ready_at`, а после `RestaurantOrderAccepted` workflow SHALL ждать в stage `scheduled` таймеров `QueueRestaurantOrder` и `CreateDelivery`. Activities MUST вызываться только из главного цикла workflow, а не из обработчиков Signals.

#### Scenario: Успешный заказ
**ID:** AC-433

- **WHEN** workflow получает Authorization, `RestaurantOrderAccepted`, `CourierAssignedToDelivery`, `RestaurantOrderReady`, `DeliveryPickedUp` и `DeliveryCompleted`
- **THEN** Activities выполняются по одному разу в порядке маршрута, и workflow завершается Completion

#### Scenario: Отказ Authorization
**ID:** AC-434

- **WHEN** `payments` отклоняет Authorization
- **THEN** workflow вызывает FailOrCancelOrder с видом failure и кодом `payment_declined` без компенсаций

#### Scenario: Успешный запланированный заказ
**ID:** AC-917

- **WHEN** workflow запланированного заказа получает Authorization и `RestaurantOrderAccepted`
- **THEN** он подтверждает резервацию, считает план, запрашивает ресторан с окном и до `start_at` не вызывает `QueueRestaurantOrder`, а до `courier_request_at` — `CreateDelivery`

### Requirement: Deadlines берутся из Policy во входе workflow
**ID:** FR-416

Каждое ожидание SHALL ограничиваться deadline из `Policy`, переданной во входе workflow. `PreparationDelayed` SHALL продлевать deadline Preparation до сообщённого времени, но не дальше предела Policy. Для запланированного заказа deadline Preparation SHALL отсчитываться от `QueueRestaurantOrder`, а ожидание в stage `scheduled` MUST NOT ограничиваться deadline Policy.

#### Scenario: Истёк срок Acceptance
**ID:** AC-435

- **WHEN** `RestaurantOrderAccepted` не приходит до deadline Acceptance
- **THEN** workflow отменяет Restaurant Order и Authorization и завершает Order кодом `restaurant_acceptance_timeout`

#### Scenario: Задержка приготовления продлевает срок
**ID:** AC-436

- **WHEN** до deadline Preparation приходит `PreparationDelayed` с более поздним ожидаемым временем
- **THEN** компенсация по исходному deadline не запускается

#### Scenario: Запланированный заказ ждёт часами
**ID:** AC-918

- **GIVEN** кухня приняла запланированный заказ за три часа до `start_at`
- **WHEN** проходит время больше deadline Preparation
- **THEN** компенсация не запускается, а deadline Preparation начинает отсчёт только после `QueueRestaurantOrder`

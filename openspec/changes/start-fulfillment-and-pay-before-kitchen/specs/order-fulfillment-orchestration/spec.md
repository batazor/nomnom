# Spec Delta

## MODIFIED Requirements

### Requirement: Основной старт через OrderPlaced

После атомарной записи `Order` и события `OrderPlaced` в Transactional Outbox контекст `orders` SHALL получить собственное событие через Kafka и Transactional Inbox, после чего выполнить `SignalWithStart` для `order:<order_id>` с конфликтной политикой `USE_EXISTING`. Это единственный путь старта Order Fulfillment Workflow. Для `Order` в `completed` или `cancelled` сигнал MUST NOT отправляться. Ошибка Temporal SHALL оставлять сообщение неподтверждённым для повторной доставки и MUST NOT отправлять его в DLQ. Собственные `OrderCompleted` и `OrderCancelled` того же топика consumer `orders` SHALL подтверждать без действия.

#### Scenario: Событие получено до явного старта
- **WHEN** `OrderPlaced` первым достигает application handler
- **THEN** `SignalWithStart` создаёт workflow `order:<order_id>` и применяет событие к нему

#### Scenario: Повторная доставка OrderPlaced
- **WHEN** Kafka повторно доставляет тот же `event_id`
- **THEN** Inbox превращает повтор в no-op и второй workflow не создаётся

#### Scenario: Сбой между commit Inbox и SignalWithStart
**ID:** AC-819

- **GIVEN** Inbox зафиксировал `event_id` `OrderPlaced`, а процесс остановился до `SignalWithStart`
- **WHEN** Kafka повторно доставляет то же сообщение
- **THEN** Inbox распознаёт дубликат, после commit выполняется `SignalWithStart`, и workflow `order:<order_id>` запущен

#### Scenario: OrderPlaced завершённого заказа
**ID:** AC-820

- **WHEN** consumer получает `OrderPlaced` для `Order` в `completed` или `cancelled`, например после redrive DLQ
- **THEN** сообщение подтверждается, `SignalWithStart` не выполняется и workflow не создаётся заново

#### Scenario: Temporal недоступен
**ID:** AC-821

- **WHEN** `SignalWithStart` завершается ошибкой Temporal
- **THEN** сообщение не подтверждается и доставляется повторно, в `orders.v1.order-events.dlq` ничего не записано

#### Scenario: Собственные завершающие события
**ID:** AC-822

- **WHEN** consumer `orders` получает `OrderCompleted` или `OrderCancelled`
- **THEN** сообщение подтверждается без бизнес-действия и не попадает в DLQ

## ADDED Requirements

### Requirement: Ресторан запрашивается после фиксации оплаты
**ID:** FR-636

Workflow SHALL запрашивать Restaurant Order (`RequestRestaurantOrder`) только после одобренной Authorization и записи факта оплаты в `Order`. Restaurant Order MUST создаваться только этим шагом; при отказе оплаты Restaurant Order MUST NOT появляться.

#### Scenario: Оплата одобрена
**ID:** AC-823

- **WHEN** `payments` одобряет Authorization
- **THEN** workflow записывает факт оплаты в `Order`, затем запрашивает Restaurant Order, и кухня видит заказ только после этого

#### Scenario: Оплата отклонена
**ID:** AC-824

- **WHEN** `payments` отклоняет Authorization
- **THEN** workflow завершает `Order` с reason `payment_declined`, Restaurant Order не создан, компенсация ресторана не требуется

### Requirement: Оплата фиксируется в Order шагом workflow
**ID:** FR-637

После одобренной Authorization workflow SHALL выполнить шаг `RecordPaymentAuthorization`: Activity вызывает use case Record Payment Authorization слайса `order` с моментом решения. Шаг MUST быть идемпотентным при повторе Activity и не имеет компенсации.

#### Scenario: Повтор Activity
**ID:** AC-825

- **WHEN** Activity `RecordPaymentAuthorization` повторяется после того, как факт уже записан
- **THEN** шаг завершается успешно, второй факт не записан, маршрут переходит к `RequestRestaurantOrder`

## REMOVED Requirements

### Requirement: Допустим синхронный старт после commit
**Reason**: Старт после commit терял процесс при сбое между commit и стартом и давал второй путь старта. Workflow запускается только consumer'ом собственного `OrderPlaced`.
**Migration**: Place Order больше не вызывает Temporal; поле `fulfillment_started` удалено из `PlaceOrderResponse`. Заказы без workflow запускаются consumer'ом при первом чтении retained `OrderPlaced`.

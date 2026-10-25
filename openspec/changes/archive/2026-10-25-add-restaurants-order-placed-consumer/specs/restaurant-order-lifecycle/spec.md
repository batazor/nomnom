# Spec Delta

## ADDED Requirements

### Requirement: OrderPlaced создаёт Restaurant Order через Inbox
**ID:** FR-495

`restaurants` SHALL потреблять `nomnom.orders.v1.OrderPlaced` из `orders.v1.order-events` и выполнять по нему Receive Order Request с `restaurant_id`, `order_id` и составом `items`. Проверка `(consumer, event_id)`, запись Inbox и сохранение Restaurant Order MUST фиксироваться в одной MongoDB-транзакции; повторная доставка того же `event_id` MUST NOT выполнять бизнес-действие повторно. RPC `ReceiveOrderRequest` и consumer MUST сходиться к одному Restaurant Order по `order_id`.

#### Scenario: Заказ оформлен
**ID:** AC-563

- **WHEN** consumer получает `OrderPlaced` открытого ресторана
- **THEN** создан Restaurant Order в `RECEIVED` с позициями события, Inbox содержит `event_id`, offset подтверждён

#### Scenario: Повторная доставка
**ID:** AC-564

- **WHEN** тот же `event_id` доставлен снова
- **THEN** второй Restaurant Order не создаётся, сообщение подтверждается как duplicate

#### Scenario: Заказ уже получен по RPC
**ID:** AC-565

- **WHEN** `OrderPlaced` приходит после `ReceiveOrderRequest` того же `order_id` и состава
- **THEN** остаётся один Restaurant Order, событие обработано без ошибки

### Requirement: Ошибки обработки OrderPlaced классифицируются
**ID:** FR-496

Сообщение с битым envelope или payload, без `items` или с невалидным составом SHALL отправляться в `orders.v1.order-events.dlq` с причиной и подтверждаться. Неизвестный ресторан или конфликтующий состав уже полученного заказа SHALL подтверждаться без Restaurant Order с записью в log. Временная ошибка SHALL повторяться с backoff, а после исчерпания повторов сообщение MUST NOT подтверждаться.

#### Scenario: OrderPlaced без состава
**ID:** AC-566

- **WHEN** consumer получает `OrderPlaced` без `items`
- **THEN** сообщение в DLQ с `reason_poisoned`, Restaurant Order и запись Inbox не созданы

### Requirement: Consumer следует runtime contract
**ID:** FR-497

`restaurants-consumer` SHALL не стартовать без `MONGODB__URI` или `KAFKA__BROKERS`, отдавать health на техническом порту и по `SIGTERM` завершать начатое сообщение в пределах drain timeout до отключения от Kafka.

#### Scenario: SIGTERM во время обработки
**ID:** AC-567

- **WHEN** процесс получает `SIGTERM`, пока сообщение обрабатывается
- **THEN** отключение от Kafka ждёт завершения обработки, offset обработанного сообщения коммитится

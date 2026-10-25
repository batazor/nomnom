# order-persistence Specification

## Purpose
Определяет хранение `Order`, транзакционную границу, Transactional Inbox и Outbox, read models и authorization revision контекста `orders` в PostgreSQL, общем для всех процессов `orders`.

## Requirements

### Requirement: Order хранится в PostgreSQL с optimistic concurrency
**ID:** FR-437

`order.Repository` SHALL сохранять `Order`, позиции и Fulfillment Facts в PostgreSQL. `Save` MUST отклонять запись, если версия в хранилище отличается от загруженной.

#### Scenario: Конкурентное изменение
**ID:** AC-468

- **WHEN** два процесса загрузили одну версию `Order` и оба сохраняют изменения
- **THEN** первое сохранение проходит, второе получает `ErrConcurrencyConflict`

### Requirement: Бизнес-изменение и Outbox фиксируются одной транзакцией
**ID:** FR-438

Изменение `Order`, запись Inbox и Integration Events SHALL фиксироваться одной транзакцией PostgreSQL. Forwarder SHALL доставлять события в Kafka `orders.v1.order-events` с ключом `order_id` и стабильным `event_id`.

#### Scenario: Откат use case
**ID:** AC-469

- **WHEN** use case возвращает ошибку после `Append`
- **THEN** ни `Order`, ни событие Outbox не сохраняются

#### Scenario: Доставка OrderPlaced
**ID:** AC-470

- **WHEN** `placeorder` зафиксировал заказ
- **THEN** Forwarder публикует `nomnom.orders.v1.OrderPlaced` с envelope `event_id`, `event_type` и ключом `order_id`

### Requirement: Процессы orders используют общий store
**ID:** FR-439

API, worker, consumer и forwarder SHALL работать с одной PostgreSQL-базой `orders`; без `POSTGRES__DSN` процесс MUST NOT стартовать.

#### Scenario: Worker исполняет заказ, оформленный другим процессом
**ID:** AC-471

- **WHEN** заказ сохранён одним процессом, а worker исполняет его workflow
- **THEN** Activities читают и завершают этот заказ в PostgreSQL

### Requirement: Authorization revision хранится с Order
**ID:** FR-440

ZedToken creation-time relationship SHALL сохраняться в строке `Order` в транзакции оформления, а проверки этого `Order` SHALL использовать его как `at_least_as_fresh`.

#### Scenario: Проверка после рестарта процесса
**ID:** AC-472

- **WHEN** после оформления заказа процесс перезапущен и клиент запрашивает заказ
- **THEN** проверка в SpiceDB использует сохранённую revision

### Requirement: OrderPlaced несёт состав заказа
**ID:** FR-498

Integration Event `nomnom.orders.v1.OrderPlaced` SHALL содержать позиции заказа `items` (`menu_item_id`, `quantity`, `unit_price`) по ценам catalog snapshot, чтобы consumers могли создать локальное представление заказа без синхронного вызова `orders`.

#### Scenario: Состав в событии
**ID:** AC-568

- **WHEN** Forwarder публикует `OrderPlaced` заказа из двух позиций
- **THEN** payload содержит обе позиции с количеством и ценой

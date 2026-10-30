# integration-event-publication Specification

## Purpose
Определяет публикацию Integration Events контекстов через Transactional Outbox и CDC на Debezium (ADR 13, ADR 31, ADR 37): какие Domain Events становятся Integration Events, как формируется запись Outbox, как коннектор доставляет её в Kafka с сохранением порядка, формат Event Envelope, retention и развёртывание коннекторов.

## Requirements

### Requirement: Integration Event записывается в Outbox транзакцией бизнес-изменения
**ID:** FR-520

`payments`, `delivery` и `restaurants` SHALL записывать Integration Event в Outbox своего хранилища (`outbox` PostgreSQL `payments`, `delivery_outbox` PostgreSQL `delivery`, коллекция `outbox` MongoDB `restaurants`) в той же локальной транзакции, что и изменение aggregate. Use case MUST NOT публиковать событие в Kafka напрямую; основной процесс контекста MUST NOT подключаться к Kafka.

#### Scenario: Commit фиксирует состояние и запись Outbox вместе
**ID:** AC-600

- **WHEN** Authorize Payment, Confirm Pickup или Accept Order успешно фиксирует изменение aggregate
- **THEN** в Outbox появляется ровно одна неопубликованная запись соответствующего Integration Event с пустым `published_at`

#### Scenario: Откат не оставляет записи Outbox
**ID:** AC-601

- **WHEN** транзакция бизнес-изменения откатывается
- **THEN** ни изменение aggregate, ни запись Outbox не сохраняются, и в журнале нечего публиковать

### Requirement: Публикуемые события выбираются явным allowlist
**ID:** FR-521

Mapper Domain Event → Integration Event SHALL находиться в `infrastructure` и сопоставлять только события с Protobuf-контрактом. Domain Event вне allowlist MUST NOT попадать в Outbox. Основной процесс MUST подключать mapper контекста, тестовая сборка хранилища — mapper без контрактов. В `restaurants` allowlist ведёт слайс aggregate, mapper процесса MUST выбирать его по `aggregate_type`, неизвестный тип MUST NOT публиковаться.

#### Scenario: Внутренний факт payments не публикуется
**ID:** AC-602

- **WHEN** провайдер отклоняет операцию и `Payment` записывает `PaymentOperationDeclined`
- **THEN** Outbox не содержит записи для этого события

#### Scenario: Внутренние факты restaurants вне allowlist не публикуются
**ID:** AC-603

- **WHEN** транзакция `restaurants` записывает `RestaurantOrderReceived`, `RestaurantOrderQueued`, `RestaurantOrderAssembled`, `RestaurantRegistered`, `RestaurantRenamed` или `MenuCreated` (внутренние события `Restaurant` и `Menu` без контракта; `RestaurantOpened`, `RestaurantClosed`, `MenuItemAdded`, `MenuItemUpdated` и `MenuItemAvailabilityChanged` публикуются по FR-560, FR-561 и FR-613)
- **THEN** коллекция `outbox` не пополняется для этих событий

#### Scenario: Новый Domain Event delivery требует решения о публикации
**ID:** AC-604

- **WHEN** в `DomainEvent` слайса `delivery` добавлен вариант без ветки в `DeliveryIntegrationEvents`
- **THEN** сборка не проходит, пока вариант не сопоставлен с контрактом или явно не исключён

### Requirement: Integration Events payments
**ID:** FR-522

`payments` SHALL публиковать в topic `payments.v1.payment-events` по контракту `nomnom/payments/v1/events.proto`: `PaymentAuthorized`, `PaymentCaptured`, `PaymentAuthorizationCancelled`, `PaymentRefunded` с полями `payment_id`, `order_id`, `amount` (`Money`: строковая точная сумма и валюта) и `PaymentFailed` с `payment_id`, `order_id`, `reason`. `aggregate_type` записи MUST быть `Payment`, `event_type` — полное имя сообщения (`nomnom.payments.v1.*`).

#### Scenario: Authorization публикуется с суммой
**ID:** AC-605

- **WHEN** Authorize Payment фиксирует успешную Authorization
- **THEN** Outbox содержит `nomnom.payments.v1.PaymentAuthorized` с `payment_id`, `order_id` и авторизованной суммой в topic `payments.v1.payment-events`

#### Scenario: Неудача Payment публикуется с причиной
**ID:** AC-606

- **WHEN** `Payment` завершается неудачей по бизнес-причине
- **THEN** Outbox содержит `nomnom.payments.v1.PaymentFailed` с этой `reason`

### Requirement: Integration Events delivery
**ID:** FR-523

`delivery` SHALL публиковать в topic `delivery.v1.delivery-events` по контракту `nomnom/delivery/v1/events.proto` каждое Domain Event aggregate `Delivery` (`aggregate_type` = `delivery`): `CourierAssignedToDelivery` и `CourierUnassignedFromDelivery` (`order_id`, `delivery_id`, `courier_id`), `DeliveryPickedUp` и `DeliveryCompleted` (`order_id`, `delivery_id`), `DeliveryFailed` и `DeliveryCancelled` (`order_id`, `delivery_id`, `reason`).

#### Scenario: Отмена без причины
**ID:** AC-608

- **WHEN** Cancel Delivery выполняется без причины
- **THEN** `nomnom.delivery.v1.DeliveryCancelled` содержит пустой `reason`

### Requirement: delivery не публикует курьера сверх контракта
**ID:** FR-534

`courier_id` событий Pickup, Completion и Failure и освобождённый при отмене курьер MUST NOT входить в Integration Events `delivery`. События курьерского слайса (`courier`) MUST NOT публиковаться.

#### Scenario: Pickup публикуется без курьера
**ID:** AC-607

- **WHEN** назначенный курьер подтверждает Pickup
- **THEN** Outbox содержит `nomnom.delivery.v1.DeliveryPickedUp` только с `order_id` и `delivery_id`

### Requirement: Integration Events restaurants
**ID:** FR-524

`restaurants` SHALL публиковать в topic `restaurants.v1.restaurant-order-events` по контракту `nomnom/restaurants/v1/events.proto` события aggregate `RestaurantOrder`: `RestaurantOrderAccepted`, `RestaurantOrderRejected`, `PreparationStarted`, `PreparationDelayed`, `PreparationFailed` (Domain Event `RestaurantOrderFailed`), `RestaurantOrderReady`, `RestaurantOrderCancelled`; каждое MUST содержать `order_id`. События `Restaurant` и `Menu` MUST идти в свои стримы (FR-560, FR-561).

#### Scenario: Неудача приготовления публикуется как PreparationFailed
**ID:** AC-609

- **WHEN** кухня фиксирует Report Preparation Failure и aggregate записывает `RestaurantOrderFailed`
- **THEN** Outbox содержит `nomnom.restaurants.v1.PreparationFailed` с `order_id` и `reason`

### Requirement: Поля фактов restaurants
**ID:** FR-535

`RestaurantOrderRejected`, `PreparationFailed` и `RestaurantOrderCancelled` SHALL нести `reason`; отсутствующая причина MUST передаваться пустой строкой. `RestaurantOrderCancelled` MUST нести `preparation_started`, `PreparationDelayed` — `expected_ready_at` как `google.protobuf.Timestamp`.

#### Scenario: Отмена после начала приготовления
**ID:** AC-610

- **WHEN** Cancel Restaurant Order отменяет заказ, Preparation которого уже начата, без причины
- **THEN** `nomnom.restaurants.v1.RestaurantOrderCancelled` содержит `preparation_started = true` и пустой `reason`

#### Scenario: Задержка передаёт новую готовность
**ID:** AC-611

- **WHEN** кухня сообщает Report Preparation Delay с новым `expected_ready_at`
- **THEN** `nomnom.restaurants.v1.PreparationDelayed` содержит этот момент как Timestamp

### Requirement: Состав записи Outbox
**ID:** FR-525

Запись Outbox SHALL содержать `event_id`, `event_type` (полное имя Protobuf-сообщения), `aggregate_type`, `aggregate_id`, `aggregate_version` (версия, записанная той же транзакцией), `occurred_at`, `topic`, `payload` (сериализованный Protobuf контракта), `metadata` (`traceparent` при наличии span), `seq`, `published_at`, `attempts`, `last_error`.

#### Scenario: Запись после Accept Order
**ID:** AC-625

- **WHEN** Accept Order фиксирует принятие заказа версии 2
- **THEN** запись Outbox имеет `event_type = nomnom.restaurants.v1.RestaurantOrderAccepted`, `aggregate_type = RestaurantOrder`, `aggregate_version = 2`, topic стрима, Protobuf payload, `attempts = 0` и пустой `published_at`

### Requirement: Детерминированный event_id
**ID:** FR-536

`event_id` SHALL быть UUIDv5 в namespace `8f1d3c2a-5b7e-4a90-9c61-2e4f7b8d0a13` от `"{aggregate_type}:{aggregate_id}:{aggregate_version}:{event_type}"`. Уникальный индекс по `event_id` MUST откатывать транзакцию с дублем.

#### Scenario: Повторное вычисление
**ID:** AC-612

- **WHEN** одно и то же событие одной версии aggregate вычисляется повторно
- **THEN** `event_id` совпадает, а события разных `event_type` одной версии получают разные `event_id`

#### Scenario: Дубль event_id откатывает транзакцию
**ID:** AC-613

- **WHEN** транзакция пытается записать запись Outbox с уже существующим `event_id`
- **THEN** транзакция откатывается вместе с изменением aggregate

### Requirement: Outbox доставляет CDC
**ID:** FR-526

Запись Outbox SHALL доставлять в Kafka коннектор Debezium `<context>-outbox` в Kafka Connect из журнала хранилища (PostgreSQL WAL через publication `<context>_outbox` с `publish = 'insert'`, MongoDB change streams коллекции `outbox`) (ADR 37). Сервис MUST NOT публиковать Outbox сам и MUST NOT опрашивать таблицу или коллекцию Outbox.

#### Scenario: Событие в Kafka после commit
**ID:** AC-616

- **WHEN** транзакция записала изменение aggregate и запись Outbox и закоммитилась
- **THEN** коннектор контекста публикует запись в её `topic` без процесса relay

### Requirement: Запись Outbox несёт всё сообщение
**ID:** FR-527

Каждая запись Outbox SHALL содержать `event_id`, `event_type`, `aggregate_type`, `aggregate_id`, `aggregate_version`, `occurred_at`, `topic`, `payload` и `traceparent`; `traceparent` MUST быть пустой строкой без trace context, а не отсутствовать (документ MongoDB без поля останавливает коннектор). Состояния публикации (`published_at`, `attempts`, `seq` relay) MUST NOT быть.

#### Scenario: Запись без trace context
**ID:** AC-617

- **WHEN** use case пишет событие вне span
- **THEN** запись Outbox содержит `traceparent = ''`, а остальные поля контракта заполнены

### Requirement: Порядок и повтор
**ID:** FR-528

Записи одного `aggregate_id` SHALL публиковаться в порядке commit с Kafka key `aggregate_id`. Доставка at-least-once: повтор после рестарта Connect MUST быть безопасен для consumers (Inbox, ADR 11). Запись, нарушающая контракт, MUST останавливать коннектор, а не пропускаться.

#### Scenario: Запись нарушает контракт
**ID:** AC-618

- **WHEN** коннектор получает запись без поля контракта
- **THEN** задача коннектора переходит в `FAILED`, последующие записи не публикуются до исправления и restart задачи

### Requirement: Event Envelope совпадает с orders
**ID:** FR-530

Коннектор SHALL передавать Event Envelope в Kafka headers строками (`SimpleHeaderConverter`): `event_id`, `_watermill_message_uuid` (= `event_id`), `event_type`, `occurred_at` (RFC 3339 UTC), `aggregate_type`, `aggregate_id`, `aggregate_version` (десятичная строка), `traceparent` (пустой без trace context).

#### Scenario: Headers опубликованного события
**ID:** AC-620

- **WHEN** коннектор публикует запись с `occurred_at = 2026-10-25T10:15:30.120000Z` без trace context
- **THEN** сообщение несёт все headers Envelope, `occurred_at` — RFC 3339 UTC, `traceparent` пустой, а Go consumer `orders` читает `event_id` как UUID сообщения Watermill

### Requirement: Value записи стрима
**ID:** FR-724

Value стрима Integration Events SHALL быть `payload` в wire format Schema Registry (transform `SchemaFraming`, FR-714). Value Change Notification streams (`*.v1.*-changes`) MUST быть `payload` без изменений (`ByteArrayConverter`).

#### Scenario: Value Integration Event и Change Notification
**ID:** AC-981

- **WHEN** транзакция `orders` записывает Integration Event в `orders.v1.order-events` и Order Change Notification в `orders.v1.order-changes`
- **THEN** value первого — wire format с id subject его `event_type`, value второго — JSON без префикса

### Requirement: Retention записей Outbox
**ID:** FR-531

Записи Outbox SHALL удаляться через час после вставки (`created_at`) без фонового процесса: PostgreSQL-контексты — в транзакции очередной записи Outbox (`FOR UPDATE SKIP LOCKED`), MongoDB — TTL-индексом. Удаление MUST NOT попадать в поток: Debezium читает событие из журнала.

#### Scenario: Старые записи уходят со следующей записью
**ID:** AC-621

- **WHEN** в Outbox есть запись старше часа и транзакция пишет новую запись Outbox
- **THEN** после commit старой записи нет, а новая есть

### Requirement: Развёртывание коннектора
**ID:** FR-538

Helm chart каждого контекста SHALL содержать конфигурацию коннектора (`cdc.*`) и при `cdc.enabled` создавать ресурс Strimzi `KafkaConnector` `<context>-outbox` в namespace `KafkaConnect` (`nomnom-infra`, label `strimzi.io/cluster: kafka-connect`) с `autoRestart.enabled: true`. Регистрации коннектора через REST Kafka Connect MUST NOT быть. Тестовое kind-окружение SHALL включать `cdc` в основном release каждого контекста.

#### Scenario: Коннекторы в kind
**ID:** AC-624

- **WHEN** окружение kind разворачивается через Skaffold
- **THEN** `KafkaConnector` `orders-outbox`, `payments-outbox`, `delivery-outbox` и `restaurants-outbox` в `Ready`, коннекторы и их задачи в состоянии `RUNNING`, и процессов relay нет

#### Scenario: Задача восстанавливается без деплоя
**ID:** AC-987

- **WHEN** задача коннектора перешла в `FAILED` из-за записи без схемы, а затем схема зарегистрирована
- **THEN** Strimzi перезапускает задачу, и она публикует запись без повторного деплоя

### Requirement: Пароли коннектора вне конфигурации
**ID:** FR-725

Пароли и URI с паролем коннектора SHALL браться config provider Kafka Connect (`${dir:/mnt/cdc:<ключ>}` из Secret `cdc-credentials`), а не храниться в конфигурации `KafkaConnector`.

#### Scenario: Конфигурация коннектора без пароля
**ID:** AC-988

- **WHEN** release `orders` рендерит `KafkaConnector` `orders-outbox`
- **THEN** `database.password` — ссылка `${dir:/mnt/cdc:orders-password}`, а не значение пароля

### Requirement: Integration Events открытости ресторана
**ID:** FR-560

`restaurants` SHALL публиковать в topic `restaurants.v1.restaurant-events` по контракту `nomnom/restaurants/v1/events.proto` события aggregate `Restaurant` (`aggregate_type = Restaurant`, `aggregate_id` — id ресторана): `RestaurantOpened` и `RestaurantClosed` с полем `restaurant_id`. Запись MUST создаваться в той же Unit of Work, что и изменение открытости.

#### Scenario: Open Restaurant публикует RestaurantOpened
**ID:** AC-660

- **WHEN** Open Restaurant открывает закрытый ресторан и фиксирует версию 2
- **THEN** Outbox содержит `nomnom.restaurants.v1.RestaurantOpened` с `restaurant_id`, `aggregate_type = Restaurant`, `aggregate_version = 2` и topic `restaurants.v1.restaurant-events`, а коннектор публикует его с key, равным id ресторана

#### Scenario: Close Restaurant публикует RestaurantClosed
**ID:** AC-661

- **WHEN** Close Restaurant закрывает открытый ресторан
- **THEN** Outbox содержит `nomnom.restaurants.v1.RestaurantClosed` с `restaurant_id` в topic `restaurants.v1.restaurant-events`

#### Scenario: Повтор открытия не публикуется
**ID:** AC-662

- **WHEN** Open Restaurant выполняется для уже открытого ресторана
- **THEN** новая запись Outbox не создаётся

### Requirement: Integration Event доступности Menu Item
**ID:** FR-561

`restaurants` SHALL публиковать в topic `restaurants.v1.menu-events` по контракту `nomnom/restaurants/v1/events.proto` событие aggregate `Menu` (`aggregate_type = Menu`, `aggregate_id` — id меню) `MenuItemAvailabilityChanged` с полями `menu_id`, `restaurant_id`, `menu_item_id` и `available` (новое значение доступности). Событие MUST публиковаться при любой команде, изменившей доступность, и MUST NOT публиковаться при изменении других данных Menu Item.

#### Scenario: Set Menu Item Availability публикует факт
**ID:** AC-663

- **WHEN** Set Menu Item Availability делает доступное блюдо недоступным
- **THEN** Outbox содержит `nomnom.restaurants.v1.MenuItemAvailabilityChanged` с `menu_id`, `restaurant_id`, `menu_item_id` и `available = false` в topic `restaurants.v1.menu-events`, а коннектор публикует его с key, равным id меню

#### Scenario: Update Menu Item с новой доступностью
**ID:** AC-664

- **WHEN** Update Menu Item меняет название и доступность блюда
- **THEN** Outbox содержит `MenuItemAvailabilityChanged` с новым значением `available`

#### Scenario: Update Menu Item без изменения доступности
**ID:** AC-665

- **WHEN** Update Menu Item меняет только название
- **THEN** Outbox не содержит нового `MenuItemAvailabilityChanged`; изменение публикует `MenuItemUpdated` (FR-613)

### Requirement: Integration Events состава и данных Menu Item
**ID:** FR-613

`restaurants` SHALL публиковать в topic `restaurants.v1.menu-events` события aggregate `Menu` `MenuItemAdded` (Add Menu Item) и `MenuItemUpdated` (Update Menu Item изменил название, описание или цену) с полями `menu_id`, `restaurant_id` и `menu_item_id`, атомарно с меню. Новые значения соседи и кеш чтения (ADR 43) читают через GetMenu; события MUST NOT нести цену или название. Update Menu Item без изменения этих данных MUST NOT публиковать `MenuItemUpdated`.

#### Scenario: Add Menu Item публикует факт
**ID:** AC-773

- **WHEN** Add Menu Item добавляет блюдо в меню ресторана
- **THEN** Outbox содержит `nomnom.restaurants.v1.MenuItemAdded` с `menu_id`, `restaurant_id` и `menu_item_id` в topic `restaurants.v1.menu-events` в той же транзакции, что и меню

#### Scenario: Смена цены публикует MenuItemUpdated
**ID:** AC-774

- **WHEN** Update Menu Item меняет цену блюда
- **THEN** Outbox содержит `nomnom.restaurants.v1.MenuItemUpdated` с ключами блюда, а кешированный GetMenu ресторана обновляется после доставки события

### Requirement: Один стрим на aggregate restaurants
**ID:** FR-562

Каждый aggregate `restaurants`, чьи события публикуются, SHALL иметь собственный topic, чтобы порядок записей одного `aggregate_id` сохранялся внутри partition стрима: `RestaurantOrder` → `restaurants.v1.restaurant-order-events`, `Restaurant` → `restaurants.v1.restaurant-events`, `Menu` → `restaurants.v1.menu-events`. Единственный relay MUST публиковать записи всех стримов в порядке `seq`, используя topic записи.

#### Scenario: Mapper процесса разводит aggregates по стримам
**ID:** AC-666

- **WHEN** основной процесс записывает `RestaurantOpened` ресторана, `MenuItemAvailabilityChanged` меню и `PreparationStarted` Kitchen Order
- **THEN** записи Outbox получают topics `restaurants.v1.restaurant-events`, `restaurants.v1.menu-events` и `restaurants.v1.restaurant-order-events` соответственно

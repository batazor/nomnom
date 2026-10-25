# integration-event-publication Specification

## Purpose
Определяет публикацию Integration Events контекстов `payments`, `delivery` и `restaurants` через Transactional Outbox и polling relay (ADR 13, ADR 31): какие Domain Events становятся Integration Events, как формируется запись Outbox, как relay доставляет её в Kafka с сохранением порядка и подтверждением брокера, формат Event Envelope, retention, метрики, конфигурация и развёртывание relay.

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
- **THEN** ни изменение aggregate, ни запись Outbox не сохраняются, и relay нечего публиковать

### Requirement: Публикуемые события выбираются явным allowlist
**ID:** FR-521

Mapper Domain Event → Integration Event SHALL находиться в `infrastructure` и сопоставлять только события с Protobuf-контрактом. Domain Event вне allowlist MUST NOT попадать в Outbox. Основной процесс MUST подключать mapper контекста, тестовая сборка хранилища — mapper без контрактов. В `restaurants` allowlist ведёт слайс aggregate, mapper процесса MUST выбирать его по `aggregate_type`, неизвестный тип MUST NOT публиковаться.

#### Scenario: Внутренний факт payments не публикуется
**ID:** AC-602

- **WHEN** провайдер отклоняет операцию и `Payment` записывает `PaymentOperationDeclined`
- **THEN** Outbox не содержит записи для этого события

#### Scenario: Локальные факты и события Restaurant/Menu не публикуются
**ID:** AC-603

- **WHEN** транзакция `restaurants` записывает `RestaurantOrderReceived`, `RestaurantOrderQueued`, `RestaurantOrderAssembled`, `RestaurantRegistered`, `RestaurantRenamed`, `RestaurantAddressChanged`, `MenuCreated`, `MenuItemAdded` или `MenuItemUpdated` (внутренние события `Restaurant` и `Menu` без контракта)
- **THEN** коллекция `outbox` не пополняется

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

### Requirement: Единственный активный relay
**ID:** FR-526

Outbox каждого контекста SHALL читать отдельный процесс `<context>-relay` того же образа, и одновременно MUST быть активен не более одного экземпляра. Экземпляр без lock или lease MUST NOT читать Outbox. При штатной остановке relay MUST освобождать lock или lease.

#### Scenario: Две реплики
**ID:** AC-614

- **WHEN** запущены два экземпляра relay одного контекста
- **THEN** публикует только держатель lock или lease, второй остаётся standby и Outbox не читает

#### Scenario: Штатная остановка отдаёт lease
**ID:** AC-616

- **WHEN** активный relay получает `SIGTERM`
- **THEN** он завершает текущую запись, освобождает lock или удаляет свой lease, и standby не ждёт истечения срока

### Requirement: Механизм исключительности relay
**ID:** FR-537

`payments-relay` и `delivery-relay` SHALL удерживать session-level `pg_try_advisory_lock` на выделенном соединении и MUST проверять живость его сессии перед каждой записью. `restaurants-relay` SHALL владеть lease в `outbox_leases` со сроком `OUTBOX__LEASE_TTL` по времени сервера MongoDB и MUST продлевать его внутри пачки не реже трети TTL; `message.timeout.ms` MUST быть меньше TTL. Без lock или lease relay MUST остановить пачку; истёкший lease MAY захватить другой экземпляр.

#### Scenario: Failover после потери владельца
**ID:** AC-615

- **WHEN** соединение с advisory lock разорвано или lease истёк без продления
- **THEN** standby-экземпляр захватывает lock или lease и продолжает публикацию с первой неопубликованной записи, а прежний экземпляр не публикует следующую запись своей пачки

### Requirement: Выборка в порядке seq
**ID:** FR-527

Цикл relay SHALL читать до `OUTBOX__BATCH_SIZE` записей с пустым `published_at` в порядке возрастания `seq` и публиковать их по одной в этом порядке. Kafka message key MUST быть `aggregate_id`. Relay MUST NOT хранить offset: запись, зафиксированная позже записи с большим `seq`, MUST быть прочитана следующим циклом. `seq` MongoDB MUST выделяться счётчиком `outbox_counters` в той же транзакции.

#### Scenario: Записи aggregate публикуются по порядку
**ID:** AC-617

- **WHEN** в Outbox неопубликованы записи с `seq` 1, 2, 3 одного aggregate
- **THEN** relay публикует их в Kafka в порядке 1, 2, 3 с key, равным `aggregate_id`

### Requirement: Отметка только после подтверждения брокера
**ID:** FR-528

Relay SHALL публиковать запись idempotent producer'ом с `acks=all` и выставлять `published_at` только после подтверждения брокера. Повторная публикация записи после сбоя между подтверждением и отметкой MUST считаться штатной: она несёт тот же `event_id`.

#### Scenario: Сбой после подтверждения
**ID:** AC-618

- **WHEN** брокер подтвердил запись, а relay остановился до выставления `published_at`
- **THEN** следующий активный relay публикует её повторно с тем же `event_id` и затем отмечает опубликованной

### Requirement: Head-of-line остановка пачки
**ID:** FR-529

Первая неудачная публикация SHALL останавливать пачку: у записи MUST увеличиваться `attempts` и сохраняться усечённый `last_error`, записи после неё в этом цикле MUST NOT публиковаться, а следующий цикл MUST начинаться с неё же.

#### Scenario: Ошибка брокера на второй записи
**ID:** AC-619

- **WHEN** пачка содержит записи `seq` 1, 2, 3 и публикация записи 2 завершается ошибкой
- **THEN** запись 1 отмечена опубликованной, у записи 2 `attempts = 1` и заполнен `last_error`, запись 3 не опубликована, и следующий цикл начинает с записи 2

### Requirement: Event Envelope совпадает с orders
**ID:** FR-530

Relay SHALL передавать Event Envelope в Kafka headers в формате `orders/pkg/messaging/envelope.go`: `event_id`, `_watermill_message_uuid` (= `event_id`), `event_type`, `occurred_at` (UTC в формате Go `time.RFC3339Nano`: дробная часть без хвостовых нулей, суффикс `Z`), `aggregate_type`, `aggregate_id`, `aggregate_version` (десятичная строка). `traceparent` MUST передаваться только если записан в `metadata`. Payload сообщения MUST быть сериализованным Protobuf контракта без обёртки.

#### Scenario: Headers опубликованного события
**ID:** AC-620

- **WHEN** relay публикует запись с `occurred_at = 2026-10-25T10:15:30.120000Z` без `traceparent`
- **THEN** сообщение несёт все headers Envelope с `occurred_at = 2026-10-25T10:15:30.12Z`, header `traceparent` отсутствует, а Go consumer `orders` читает `event_id` как UUID сообщения Watermill

### Requirement: Retention опубликованных записей
**ID:** FR-531

Активный relay SHALL удалять записи, опубликованные раньше `now − OUTBOX__RETENTION` (по умолчанию 7 дней), не чаще своего интервала очистки. Неопубликованные записи MUST NOT удаляться независимо от возраста.

#### Scenario: Очистка не трогает backlog
**ID:** AC-621

- **WHEN** в Outbox есть опубликованная запись старше retention и неопубликованная запись того же возраста
- **THEN** после очистки остаётся только неопубликованная запись

### Requirement: Метрики relay
**ID:** FR-532

Relay SHALL экспортировать через OpenTelemetry instruments `nomnom.outbox.pending.messages` и `nomnom.outbox.oldest_pending.age` по topic (`messaging.destination.name`), счётчик неудачных попыток `nomnom.outbox.publish.retries` (`messaging.destination.name`, `nomnom.event.type`, `nomnom.error.class`), histograms `nomnom.outbox.publish.duration` и `nomnom.outbox.publish.lag` (от `occurred_at` до подтверждения брокера). Backlog MUST сообщать только активный экземпляр.

#### Scenario: Публикация и сбой видны в метриках
**ID:** AC-622

- **WHEN** активный relay публикует одну запись и не может опубликовать следующую
- **THEN** histograms duration и lag получили по наблюдению с `event_type` первой записи, `retries` увеличился на 1 с классом ошибки, а pending отражает оставшийся backlog topic

### Requirement: Конфигурация relay
**ID:** FR-533

Relay SHALL требовать хранилище (`DATABASE__URL` для `payments` и `delivery`, `MONGODB__URI` для `restaurants`) и `KAFKA__BROKERS`; пустое значение MUST завершать процесс ошибкой на старте. Relay MUST следовать runtime contract `pkg/lifecycle` (только технический порт).

#### Scenario: Relay без брокеров не стартует
**ID:** AC-623

- **WHEN** `<context>-relay` запускается с пустым `KAFKA__BROKERS` или пустым адресом хранилища
- **THEN** процесс завершается с ошибкой конфигурации до открытия портов и не переходит на in-memory адаптеры

### Requirement: Развёртывание relay
**ID:** FR-538

Helm chart каждого контекста SHALL предоставлять `values-relay.yaml` для отдельного release того же образа с `replicaCount: 1`, без Service и без применения миграций. Тестовое kind-окружение SHALL разворачивать releases `payments-relay`, `delivery-relay` и `restaurants-relay` с `KAFKA__BROKERS` кластера.

#### Scenario: Relay в kind
**ID:** AC-624

- **WHEN** окружение kind разворачивается через Skaffold
- **THEN** для каждого из трёх контекстов работает release relay с одним экземпляром, без Service, использующий базу основного release

### Requirement: Integration Events открытости ресторана
**ID:** FR-560

`restaurants` SHALL публиковать в topic `restaurants.v1.restaurant-events` по контракту `nomnom/restaurants/v1/events.proto` события aggregate `Restaurant` (`aggregate_type = Restaurant`, `aggregate_id` — id ресторана): `RestaurantOpened` и `RestaurantClosed` с полем `restaurant_id`. Запись MUST создаваться в той же Unit of Work, что и изменение открытости.

#### Scenario: Open Restaurant публикует RestaurantOpened
**ID:** AC-660

- **WHEN** Open Restaurant открывает закрытый ресторан и фиксирует версию 2
- **THEN** Outbox содержит `nomnom.restaurants.v1.RestaurantOpened` с `restaurant_id`, `aggregate_type = Restaurant`, `aggregate_version = 2` и topic `restaurants.v1.restaurant-events`, а relay публикует его с key, равным id ресторана

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
- **THEN** Outbox содержит `nomnom.restaurants.v1.MenuItemAvailabilityChanged` с `menu_id`, `restaurant_id`, `menu_item_id` и `available = false` в topic `restaurants.v1.menu-events`, а relay публикует его с key, равным id меню

#### Scenario: Update Menu Item с новой доступностью
**ID:** AC-664

- **WHEN** Update Menu Item меняет название и доступность блюда
- **THEN** Outbox содержит `MenuItemAvailabilityChanged` с новым значением `available`

#### Scenario: Update Menu Item без изменения доступности
**ID:** AC-665

- **WHEN** Update Menu Item меняет только название
- **THEN** новая запись Outbox не создаётся

### Requirement: Один стрим на aggregate restaurants
**ID:** FR-562

Каждый aggregate `restaurants`, чьи события публикуются, SHALL иметь собственный topic, чтобы порядок записей одного `aggregate_id` сохранялся внутри partition стрима: `RestaurantOrder` → `restaurants.v1.restaurant-order-events`, `Restaurant` → `restaurants.v1.restaurant-events`, `Menu` → `restaurants.v1.menu-events`. Единственный relay MUST публиковать записи всех стримов в порядке `seq`, используя topic записи.

#### Scenario: Mapper процесса разводит aggregates по стримам
**ID:** AC-666

- **WHEN** основной процесс записывает `RestaurantOpened` ресторана, `MenuItemAvailabilityChanged` меню и `PreparationStarted` Kitchen Order
- **THEN** записи Outbox получают topics `restaurants.v1.restaurant-events`, `restaurants.v1.menu-events` и `restaurants.v1.restaurant-order-events` соответственно

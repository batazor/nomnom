# Spec Delta

## MODIFIED Requirements

### Requirement: Публикуемые события выбираются явным allowlist
**ID:** FR-521

Mapper Domain Event → Integration Event SHALL находиться в `infrastructure` контекста и сопоставлять только события, у которых есть Protobuf-контракт. Domain Event вне allowlist MUST NOT попадать в Outbox: mapper возвращает отсутствие контракта, и запись не создаётся. Сборка хранилища по умолчанию (тесты) MUST использовать mapper без контрактов; основной процесс MUST подключать mapper контекста. В `restaurants` allowlist SHALL вести слайс, владеющий aggregate, а mapper процесса MUST выбирать его по `aggregate_type`; событие aggregate неизвестного типа MUST NOT публиковаться.

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

### Requirement: Integration Events restaurants
**ID:** FR-524

`restaurants` SHALL публиковать в topic `restaurants.v1.restaurant-order-events` по контракту `nomnom/restaurants/v1/events.proto` события aggregate `RestaurantOrder`: `RestaurantOrderAccepted`, `RestaurantOrderRejected`, `PreparationStarted`, `PreparationDelayed`, `PreparationFailed` (для Domain Event `RestaurantOrderFailed`), `RestaurantOrderReady`, `RestaurantOrderCancelled`. Каждое сообщение MUST содержать `order_id`. События aggregates `Restaurant` и `Menu` MUST публиковаться в собственные стримы (FR-560, FR-561), а не в стрим Kitchen Order.

#### Scenario: Неудача приготовления публикуется как PreparationFailed
**ID:** AC-609

- **WHEN** кухня фиксирует Report Preparation Failure и aggregate записывает `RestaurantOrderFailed`
- **THEN** Outbox содержит `nomnom.restaurants.v1.PreparationFailed` с `order_id` и `reason`

### Requirement: Механизм исключительности relay
**ID:** FR-537

`payments-relay` и `delivery-relay` SHALL удерживать session-level `pg_try_advisory_lock` с константным ключом контекста на выделенном соединении и MUST проверять, что сессия lock жива, перед публикацией каждой записи. `restaurants-relay` SHALL владеть lease-документом `restaurants-relay` в `outbox_leases` со сроком `OUTBOX__LEASE_TTL`, вычисленным по времени сервера MongoDB, и MUST продлевать lease внутри пачки не реже чем через треть TTL; `message.timeout.ms` producer MUST быть меньше TTL. Потеряв lock или lease, relay MUST остановить пачку до следующей записи; истёкший lease MAY захватить другой экземпляр.

#### Scenario: Failover после потери владельца
**ID:** AC-615

- **WHEN** соединение с advisory lock разорвано или lease истёк без продления
- **THEN** standby-экземпляр захватывает lock или lease и продолжает публикацию с первой неопубликованной записи, а прежний экземпляр не публикует следующую запись своей пачки

## ADDED Requirements

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

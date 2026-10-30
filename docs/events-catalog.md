# Каталог событий

Сгенерирован `make arch-generate`, вручную не правится. Источники: [`topics.yaml`](../ops/kind/manifests/messaging/topics.yaml) — topics, `events.proto` контекстов — события, [`model.c4`](architecture/model.c4) — потребители. Правила — [Domain Events](domain-events.md), разбор DLQ — [DLQ](dlq.md).

Все topics публикует Debezium из Outbox контекста-владельца ([ADR 37](adr/0037-outbox-delivery-via-debezium-cdc.md)).

| Topic | Владелец | Partitions | Потребители | DLQ |
| --- | --- | --- | --- | --- |
| `orders.v1.order-events` | `orders` | 3 | `orders-consumer` | есть |
| `orders.v1.order-relationships` | `orders` | 3 | `orders-consumer` | есть |
| `payments.v1.payment-events` | `payments` | 3 | нет | нет |
| `delivery.v1.delivery-events` | `delivery` | 3 | `delivery`, `orders-consumer` | есть |
| `restaurants.v1.restaurant-order-events` | `restaurants` | 3 | `orders-consumer`, `restaurants-consumer` | есть |
| `restaurants.v1.restaurant-events` | `restaurants` | 3 | `restaurants-consumer` | нет |
| `restaurants.v1.menu-events` | `restaurants` | 3 | `restaurants-consumer` | нет |
| `orders.v1.order-changes` | `orders` | 3 | `Cosmo Router` | нет |
| `restaurants.v1.kitchen-changes` | `restaurants` | 3 | `Cosmo Router` | нет |
| `delivery.v1.courier-changes` | `delivery` | 3 | `Cosmo Router` | нет |
| `delivery.v1.courier-pool-changes` | `delivery` | 3 | `Cosmo Router` | нет |

Топики `*-changes` — Change Notifications в JSON для Cosmo Router ([ADR 36](adr/0036-graphql-subscriptions-via-cosmo-streams.md)). `orders.v1.order-relationships` — внутренний стрим `orders` для синхронизации связей SpiceDB ([ADR 18](adr/0018-spicedb-for-authorization.md)).

## `orders.v1.order-events`

| Событие | Описание |
| --- | --- |
| `OrderPlaced` | Заказ оформлен. |
| `OrderCompleted` | Заказ успешно завершён. |
| `OrderCancelled` | Заказ завершён отрицательным итогом: отменой или неудачей. |

## `payments.v1.payment-events`

| Событие | Описание |
| --- | --- |
| `PaymentAuthorized` | Сумма авторизована у провайдера (Authorization). |
| `PaymentCaptured` | Авторизованная сумма списана (Capture). |
| `PaymentAuthorizationCancelled` | Авторизация отменена до списания (Authorization Cancellation). |
| `PaymentRefunded` | Списанная сумма возвращена (Refund). |
| `PaymentFailed` | Payment завершился неудачей по бизнес-причине. |

## `delivery.v1.delivery-events`

| Событие | Описание |
| --- | --- |
| `CourierAssignedToDelivery` | Курьер назначен на Delivery (Courier Assignment). |
| `DeliveryPickedUp` | Курьер забрал заказ (Pickup). |
| `DeliveryCompleted` | Delivery завершена (Delivery Completion). |
| `DeliveryFailed` | Delivery не удалась по бизнес-причине. |
| `CourierUnassignedFromDelivery` | Курьер снят с Delivery без замены (Courier Reassignment); Delivery вернулась в поиск курьера. Доменной причины снятия нет. |
| `DeliveryCancelled` | Delivery отменена до Pickup (Cancel Delivery); назначенный курьер, если был, освобождён. reason пуст, если причина не указана. |

## `restaurants.v1.restaurant-order-events`

| Событие | Описание |
| --- | --- |
| `RestaurantOrderAccepted` | Ресторан принял Kitchen Order (Order Acceptance); expected_ready_at — ожидаемая готовность на момент принятия (FR-609). |
| `RestaurantOrderRejected` | Ресторан отказался принять Kitchen Order. |
| `PreparationStarted` | Начато Preparation. |
| `PreparationDelayed` | Preparation задерживается; expected_ready_at — новая ожидаемая готовность. |
| `PreparationFailed` | Preparation не может быть завершено. |
| `RestaurantOrderReady` | Kitchen Order готов к Pickup. |
| `RestaurantOrderCancelled` | Kitchen Order отменён по запросу orders (Cancel Restaurant Order); preparation_started — отмена произошла после начала Preparation. |

## `restaurants.v1.restaurant-events`

| Событие | Описание |
| --- | --- |
| `RestaurantOpened` | Ресторан открыт для новых Restaurant Order (Open Restaurant). |
| `RestaurantClosed` | Ресторан закрыт для новых Restaurant Order (Close Restaurant); уже полученные и принятые заказы не отменяются. |
| `RestaurantWaitLevelChanged` | Уровень ожидания ресторана сменился (Wait Time Statistics, FR-646): elevated — текущее ожидание повышено относительно базового времени ресторана. aggregate_type RestaurantWaitTime, aggregate_id — restaurant_id. |

## `restaurants.v1.menu-events`

| Событие | Описание |
| --- | --- |
| `MenuItemAdded` | В меню добавлен Menu Item (Add Menu Item). Состав и цену соседи читают через GetMenu: событие несёт только ключи. |
| `MenuItemUpdated` | Изменились название, описание или цена Menu Item (Update Menu Item). Новые значения соседи читают через GetMenu; заказ хранит снимок цены на момент Place Order, поэтому уже оформленные заказы не меняются. |
| `MenuItemAvailabilityChanged` | Изменилась доступность Menu Item (Set Menu Item Availability или Update Menu Item с новой доступностью); available — новое значение. |

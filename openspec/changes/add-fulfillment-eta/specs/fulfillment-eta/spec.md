# Spec Delta

## ADDED Requirements

### Requirement: Срок готовности при принятии
**ID:** FR-609

`AcceptRestaurantOrder` SHALL принимать необязательный `expected_ready_at`; заданный срок MUST быть позже момента принятия, иначе `INVALID_ARGUMENT`. Без него срок SHALL считать Ready Time Estimator. `RestaurantOrderAccepted` SHALL нести `expected_ready_at` Restaurant Order. Эмулятор кухни SHALL передавать срок, равный запланированному моменту `READY`.

#### Scenario: Кухня называет срок
**ID:** AC-769

- **WHEN** кухня принимает заказ с `expected_ready_at = now + 12m`
- **THEN** Restaurant Order и `RestaurantOrderAccepted` несут этот срок

### Requirement: Сроки доставки от курьера
**ID:** FR-610

`AcceptDeliveryOffer` SHALL принимать необязательный `expected_pickup_at`, `ConfirmPickup` — необязательный `expected_delivered_at`; заданный срок MUST быть в будущем, иначе `INVALID_ARGUMENT`. Delivery SHALL хранить сроки, а `CourierAssignedToDelivery` и `DeliveryPickedUp` — нести их; не названный срок остаётся пустым. Эмулятор курьера SHALL передавать запланированные моменты Pickup и вручения.

#### Scenario: Курьер называет срок вручения
**ID:** AC-770

- **WHEN** курьер подтверждает Pickup с `expected_delivered_at`
- **THEN** `DeliveryPickedUp` несёт этот срок

### Requirement: Сроки в Order
**ID:** FR-611

`orders` SHALL сохранять последний срок готовности из `RestaurantOrderAccepted` и `PreparationDelayed`, срок Pickup из `CourierAssignedToDelivery` и срок вручения из `DeliveryPickedUp`, и отдавать их в `Order` и `orderChanged`. Новое назначение курьера SHALL заменять срок Pickup.

#### Scenario: Задержка готовки
**ID:** AC-771

- **WHEN** после `RestaurantOrderAccepted` приходит `PreparationDelayed` с более поздним сроком
- **THEN** `Order.expected_ready_at` равен новому сроку

### Requirement: Прогресс-бары заказа
**ID:** FR-612

Экран заказа SHALL показывать прогресс-бар «Готовка» от принятия до `expectedReadyAt` и «Доставка» от назначения курьера до `expectedDeliveredAt` (до Pickup — до `expectedPickupAt`), с оставшимся временем. Между событиями прогресс MUST досчитываться локальным таймером без запросов. Без срока бар MUST NOT показываться; просроченный срок SHALL показываться как «задерживается».

#### Scenario: Готовка идёт
**ID:** AC-772

- **WHEN** заказ принят, `expectedReadyAt` через 10 минут
- **THEN** экран показывает бар «Готовка» и оставшиеся 10 минут, и бар растёт без новых запросов

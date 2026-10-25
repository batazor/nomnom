# Spec Delta

## ADDED Requirements

### Requirement: payments публикует идемпотентные финансовые команды
**ID:** FR-427

`payments` SHALL предоставлять `CreatePayment` (идемпотентно по `order_id`) и `AuthorizePayment`, `CapturePayment`, `CancelAuthorization`, `RefundPayment` (идемпотентно по `idempotency_key`). Бизнес-отказ провайдера MUST возвращаться статусом `DECLINED`, а не gRPC-ошибкой.

#### Scenario: Повтор Authorization с тем же ключом
**ID:** AC-453

- **WHEN** `AuthorizePayment` повторяется с тем же `idempotency_key`
- **THEN** ответ содержит ту же операцию с `replayed = true`, и вторая Authorization не выполняется

### Requirement: restaurants принимает и отменяет заказ ресторана
**ID:** FR-428

`restaurants` SHALL предоставлять `ReceiveOrderRequest` и `CancelRestaurantOrder`, идемпотентные по `order_id`, и возвращать `address` ресторана в `GetRestaurant`. Отказ отмены по стадии MUST возвращаться `rejection_reason`.

#### Scenario: Отмена готового заказа
**ID:** AC-454

- **WHEN** `CancelRestaurantOrder` вызывается для Restaurant Order в состоянии Ready
- **THEN** ответ содержит `cancelled = false` и `rejection_reason = already_ready`

### Requirement: delivery создаёт, перепланирует и отменяет доставку
**ID:** FR-429

`delivery` SHALL предоставлять `CreateDelivery`, `ReplanDelivery` и `CancelDelivery`, адресуемые `order_id`. Cancel Delivery SHALL переводить `Waiting` или `Assigned` в `Cancelled` с освобождением курьера и публикацией `DeliveryCancelled`; после Pickup отмена MUST отклоняться бизнес-причиной.

#### Scenario: Отмена назначенной доставки
**ID:** AC-455

- **WHEN** `CancelDelivery` вызывается для Delivery в состоянии Assigned
- **THEN** Delivery переходит в `Cancelled`, курьер освобождается, повторный вызов возвращает успех

#### Scenario: Отмена после Pickup
**ID:** AC-456

- **WHEN** `CancelDelivery` вызывается после Pickup
- **THEN** ответ содержит `cancelled = false` и `rejection_reason = already_picked_up`

### Requirement: orders вызывает соседей через gRPC-клиенты
**ID:** FR-430

При `fulfillment.neighbours = grpc` worker `orders` SHALL реализовывать порты `Payments`, `Restaurants`, `Delivery` gRPC-клиентами к `INTERNAL_GATEWAY_ADDR`. Клиент MUST переводить `DECLINED` в `PaymentDecision`, `PENDING` и транспортные отказы — в retryable ошибку, ошибки валидации — в non-retryable.

#### Scenario: Отказ провайдера
**ID:** AC-457

- **WHEN** `AuthorizePayment` отвечает `DECLINED` с причиной
- **THEN** клиент возвращает `PaymentDecision{Approved: false}` с этой причиной без ошибки

#### Scenario: Ожидающая операция
**ID:** AC-458

- **WHEN** операция отвечает `PENDING`
- **THEN** клиент возвращает retryable ошибку, и Activity повторяет вызов с тем же ключом

## RENAMED Requirements

- FROM: `### Requirement: delivery создаёт, перепланирует и отменяет доставку`
- TO: `### Requirement: delivery создаёт и отменяет доставку`

## MODIFIED Requirements

### Requirement: delivery создаёт и отменяет доставку
**ID:** FR-429

`delivery` SHALL предоставлять `CreateDelivery` и `CancelDelivery`, адресуемые `order_id`. Перепланирования доставки по запросу `orders` нет: поиском курьера владеет Assignment Workflow `delivery`. Cancel Delivery SHALL переводить `Waiting` или `Assigned` в `Cancelled` с освобождением курьера и публикацией `DeliveryCancelled`; после Pickup отмена MUST отклоняться бизнес-причиной.

#### Scenario: Отмена назначенной доставки
**ID:** AC-455

- **WHEN** `CancelDelivery` вызывается для Delivery в состоянии Assigned
- **THEN** Delivery переходит в `Cancelled`, курьер освобождается, повторный вызов возвращает успех

#### Scenario: Отмена после Pickup
**ID:** AC-456

- **WHEN** `CancelDelivery` вызывается после Pickup
- **THEN** ответ содержит `cancelled = false` и `rejection_reason = already_picked_up`

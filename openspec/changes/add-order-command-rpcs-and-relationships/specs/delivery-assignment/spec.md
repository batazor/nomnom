# Spec Delta

## ADDED Requirements

### Requirement: Публикация снятия курьера и отмены Delivery
**ID:** FR-475

`delivery` SHALL публиковать Domain Events `CourierUnassignedFromDelivery` (`order_id`, `delivery_id`, `courier_id`) и `DeliveryCancelled` (`order_id`, `delivery_id`, `reason`) как Integration Events `nomnom.delivery.v1.*` стрима `delivery.v1.delivery-events` через Outbox той же транзакции. Отсутствующая причина отмены MUST передаваться пустым `reason`. Контракт MUST меняться только аддитивно.

#### Scenario: Снятие курьера и отмена попадают в Outbox
**ID:** AC-532

- **WHEN** Reassign Courier или Cancel Delivery сохраняет Delivery
- **THEN** Outbox содержит `CourierUnassignedFromDelivery` или `DeliveryCancelled` с полями контракта, а relay публикует их с envelope headers ADR 31

#### Scenario: События одной версии агрегата различимы
**ID:** AC-533

- **WHEN** одна транзакция пишет несколько событий одного `aggregate_version` (например, снятие и назначение курьера)
- **THEN** их `event_id` различаются, потому что UUIDv5 envelope включает `event_type`

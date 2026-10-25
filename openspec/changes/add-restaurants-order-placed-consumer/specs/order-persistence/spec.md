# Spec Delta

## ADDED Requirements

### Requirement: OrderPlaced несёт состав заказа
**ID:** FR-498

Integration Event `nomnom.orders.v1.OrderPlaced` SHALL содержать позиции заказа `items` (`menu_item_id`, `quantity`, `unit_price`) по ценам catalog snapshot, чтобы consumers могли создать локальное представление заказа без синхронного вызова `orders`.

#### Scenario: Состав в событии
**ID:** AC-568

- **WHEN** Forwarder публикует `OrderPlaced` заказа из двух позиций
- **THEN** payload содержит обе позиции с количеством и ценой

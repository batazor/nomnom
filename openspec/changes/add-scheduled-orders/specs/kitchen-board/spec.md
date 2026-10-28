# Spec Delta

## ADDED Requirements

### Requirement: Запланированные заказы видны кухне
**ID:** FR-684

`restaurants` SHALL предоставлять query List Scheduled Orders и публиковать его по gRPC как `ListScheduledOrders(restaurant_id)`: заказы ресторана в `ACCEPTED` с окном по возрастанию `planned_ready_at`, с `restaurant_order_id`, `order_id`, позициями, окном и `planned_ready_at`. Query MUST требовать `restaurant#operate_kitchen`. Запланированные заказы MUST NOT попадать в Get Kitchen Queue до постановки в очередь.

#### Scenario: Принятый заказ ко времени
**ID:** AC-925

- **WHEN** кухня приняла заказ на 19:00 и запрашивает запланированные и очередь
- **THEN** заказ есть в List Scheduled Orders и отсутствует в Get Kitchen Queue

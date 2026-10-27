# Spec Delta

## REMOVED Requirements

### Requirement: OrderPlaced создаёт Restaurant Order через Inbox
**Reason**: Restaurant Order из `OrderPlaced` появлялся до авторизации оплаты: кухня могла принять заказ раньше шага workflow, а неоплаченный заказ оставался на кухне. Restaurant Order создаётся только RPC `ReceiveOrderRequest` из шага workflow после оплаты.
**Migration**: Consumer `OrderPlaced` и его маршрут в `restaurants-consumer` удаляются; процесс продолжает читать `delivery.v1.delivery-events` и обновлять кеш каталога. Записи Inbox этого consumer'а истекают по TTL.

### Requirement: Ошибки обработки OrderPlaced классифицируются
**Reason**: `restaurants` больше не потребляет `OrderPlaced`.
**Migration**: Классификация ошибок остаётся у consumers `delivery.v1.delivery-events` и обновления кеша; `orders.v1.order-events.dlq` теперь получает только группа `orders`.

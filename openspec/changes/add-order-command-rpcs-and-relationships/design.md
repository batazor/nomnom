# Design

## Command RPC

Только два use case имеют внешних callers, поэтому только они попадают в `OrdersService`:

| Use case | Caller | gRPC |
| --- | --- | --- |
| Place Order | клиент | `PlaceOrder` |
| Request Order Cancellation | клиент или авторизованный actor | `RequestOrderCancellation` |
| Confirm/Reject Order, Record … | Kafka consumer | нет |
| Relay Preparation Issue | Kafka consumer | нет |
| Complete Order, Fail or Cancel Order | Temporal workflow | нет |

Messages находятся в `service.proto`, рядом с RPC: `PlaceOrderRequest{idempotency_key, restaurant_id, items, delivery_address}`, `RequestOrderCancellationRequest{order_id, request_id, reason}` и enum `CancellationRequestOutcome`. Поля customer нет: Customer — verified subject. Пока resolver выдаёт только service subjects (ADR 27), Place Order отвечает `PERMISSION_DENIED`, как command RPC соседей до BFF identity.

Зависимости API:

- `placeorder.Catalog` — `neighbours.UnavailableCatalog`: без API меню `restaurants` цены не проверить, поэтому adapter fail-closed с `ErrDependencyUnavailable`.
- Temporal — `client.NewLazyClient`: процесс стартует и становится ready без Temporal; Place Order сохраняет заказ с `fulfillment_started=false` (повтор с тем же ключом повторяет start), Request Order Cancellation отвечает `UNAVAILABLE`.
- `system.Clock` и `system.OrderIDs` (UUIDv7: упорядочены по времени и валидны как SpiceDB object id).
- Cancellation policy — тот же экземпляр `NewCancellationPolicy`, что у Get Order.

Маппинг ошибок (порядок важен: fail-closed check оборачивает недоступность SpiceDB в `ErrPermissionDenied`, но вызывающий должен получить retryable `UNAVAILABLE`):

| Ошибка | gRPC | reason |
| --- | --- | --- |
| `ErrDependencyUnavailable` (в т.ч. `spicedb.ErrUnavailable`, catalog), `fulfillment.ErrWorkflowNotFound` | `UNAVAILABLE` | `DEPENDENCY_UNAVAILABLE` |
| `vo.ErrInvalidValue`, `NotOrderable`, `ErrOrderRequiresItems`, `ErrDuplicateMenuItem` | `INVALID_ARGUMENT` | `INVALID_ORDER` |
| `InvalidTransition`, `CancellationNotAllowed`, `fulfillment.ErrCancellationRefused` | `FAILED_PRECONDITION` | `ORDER_STATE_CONFLICT` |

## Relationships

```text
definition order {
	relation customer: user
	relation restaurant: restaurant
	relation courier: user
	relation delivery: delivery

	permission view = customer + restaurant->view_orders + courier + delivery->act_as_courier
	permission cancel = customer
}
```

`courier_id` в событиях `delivery` — id пользователя (`delivery#courier@user`), поэтому `order#courier` ссылается на `user`. `delivery->act_as_courier` даёт доступ и тогда, когда `delivery` уже переназначил курьера, а `orders` ещё не обработал событие.

### Placement

`order#customer` и `order#restaurant` — creation-time relationships: один `WriteRelationships` до local commit, ZedToken хранится в `authorization_revision`. Ошибка записи — fail closed, неудавшийся commit оставляет orphan для reconciliation.

### Courier Assignment

`CourierAssignedToDelivery{order_id, delivery_id, courier_id}` обрабатывается Record Courier Assignment. Relationship write выполняется внутри Inbox unit of work до commit (`docs/security.md`, dual-write): ошибка SpiceDB откатывает Inbox record, и redelivery повторяет событие; `TOUCH`/`DELETE` идемпотентны.

```text
UoW { Inbox.Accept → Order.RecordCourierAssignment → Save
      → SELECT assignment FOR UPDATE
      → skip if stored.assigned_at > occurred_at
      → SpiceDB { TOUCH courier, delivery; DELETE previous if different }
      → UPDATE delivery_id, courier_id, courier_assigned_at, authorization_revision }
commit → Notify workflow
```

`Reflector.ReflectWith` выполняет шаг и для `already_applied`: факт Courier Assignment записывается один раз, а повторное назначение (reassignment) меняет только relationships и хранимое назначение. Для duplicate и late (финальный `Order`) шаг не выполняется. Событие без `delivery_id`/`courier_id` отражает факт без relationships.

Reassignment — один атомарный запрос: заказ никогда не виден обоим курьерам или никому. Revision сохраняется и после `DELETE`, поэтому отзыв доступа виден следующей проверке.

### Reconciliation

`reconcile.Relations` перечисляет `customer`, `restaurant`, `courier`, `delivery` и object type subject. Для каждой relation выполняется тот же проход: graph → business (orphan/stale удаляется, только если остался таким на двух проходах с интервалом ≥ grace), затем business → graph (`TOUCH` missing с обновлением `authorization_revision`). Business-сторона — колонка строки `orders` из закрытого списка `postgres.Relation`; `NULL` (курьера ещё нет) означает отсутствие ожидаемого subject.

## Known gap

`delivery` не публикует `CourierUnassignedFromDelivery` и `DeliveryCancelled`. Снятие курьера без назначения нового не наблюдаемо для `orders`: `order#courier` прежнего курьера остаётся до следующего назначения. Путь `delivery->act_as_courier` при этом корректен, потому что `delivery#courier` пишет владелец. Закрыть разрыв может только новое integration event `delivery`.

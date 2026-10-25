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

## Снятие курьера и отмена Delivery

`delivery` публикует `CourierUnassignedFromDelivery {order_id, delivery_id, courier_id}` (доменной причины снятия нет) и `DeliveryCancelled {order_id, delivery_id, reason}` (пустой `reason` — причина не указана). Все события одной транзакции имеют один `aggregate_version`; `event_id` различается через `event_type` (ADR 31), поэтому снятие и назначение в одной версии не сталкиваются. Reassign Courier сейчас только снимает курьера (`assigned → waiting`); новое назначение — отдельная транзакция Accept Delivery Offer.

Оба события обрабатывает новый use case Record Courier Release (`orders.recordcourierrelease`) без `fulfillment.Reflector`:

```text
UoW { Inbox.Accept → CourierAssignment FOR UPDATE
      → skip (ignored_stale), если delivery_id другой, назначение новее события
        или (для снятия) courier_id другой
      → SpiceDB { DELETE order#courier; DELETE order#delivery при отмене }
      → UPDATE courier_id = NULL (delivery_id = NULL при отмене),
               courier_assigned_at = occurred_at, authorization_revision }
commit
```

Решения:

- Новый use case, а не ветка Record Courier Assignment: ни одно событие не является fulfillment-фактом `Order`. Отмену Delivery запрашивает сам Order Fulfillment workflow (компенсация `CancelDelivery`), а снятие курьера возвращает Delivery в поиск; `recorddeliveryfailure` (`DeliveryFailed`) — другой бизнес-исход после Pickup. Поэтому Order и workflow не меняются и не уведомляются.
- `courier_assigned_at` после снятия хранит время снятия: более старое `CourierAssignedToDelivery`, доставленное позже, пропускается тем же правилом «старше сохранённого». Строка с `courier_assigned_at`, но без `courier_id`, читается как назначение без курьера; `AssignCourier` не удаляет пустой прежний subject.
- Reconciler не меняется: `NULL` колонки — отсутствующая relation, поэтому остаток в SpiceDB удаляется как orphan после grace.

## Known gap

Закрыт: снятие курьера без замены и отмена Delivery наблюдаемы через `CourierUnassignedFromDelivery`/`DeliveryCancelled` (FR-475…FR-477). Остаётся: событие, пришедшее раньше `CourierAssignedToDelivery` той же Delivery (нарушение порядка внутри partition невозможно, но возможно после ручного replay), пропускается как `ignored_stale`, и последующее назначение снова выдаёт доступ до следующего снятия.

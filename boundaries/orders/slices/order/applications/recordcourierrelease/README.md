# Record Courier Release

## Сценарий

- **Тип:** Command.
- **Actor:** Kafka consumer `orders`.
- **Намерение:** Отозвать доступ курьера к заказу, когда `delivery` снимает его без замены или отменяет Delivery.
- **Результат:** `order#courier` (и `order#delivery` при отмене) удалены, если событие относится к сохранённому назначению.

## Границы

- **Владелец:** `orders/slices/order`.
- **Домен:** `domains/order` не меняется: снятие курьера и отмена Delivery не являются fulfillment-фактами `Order`.
- **Взаимодействие:** Принимает integration events `CourierUnassignedFromDelivery` и `DeliveryCancelled` от `delivery` через Transactional Inbox; workflow не уведомляется.

## Последовательность

```mermaid
sequenceDiagram
    participant Kafka
    participant Inbox as Transactional Inbox
    participant App as Use case
    participant Store as orders (назначение)
    participant SpiceDB
    Kafka->>Inbox: integration event
    Inbox->>Inbox: дедупликация
    Inbox->>App: снятие / отмена
    App->>Store: назначение FOR UPDATE
    App->>SpiceDB: DELETE order#courier [, order#delivery]
    App->>Store: courier_id = NULL [, delivery_id = NULL], время, revision
    Inbox-->>Kafka: ack после commit
```

## Реализация

Handler: [`recordcourierrelease.go`](recordcourierrelease.go), unit-тесты: [`recordcourierrelease_test.go`](recordcourierrelease_test.go).

- **Поведение:** `Input{EventID, OrderID, OccurredAt, DeliveryID, CourierID, DeliveryCancelled}`. В одном unit of work: `Inbox.Accept`, чтение назначения под row lock, затем `Relationships.ReleaseCourier` и `Assignments.RememberCourierAssignment` с пустым курьером (и Delivery при отмене), временем события и ZedToken.
- **Guard:** событие применяется, только если сохранённое назначение относится к той же Delivery, к тому же курьеру (для снятия) и не новее события; иначе `ignored_stale` без изменений, но с Inbox record. Время снятия сохраняется в `courier_assigned_at`, поэтому более старое `CourierAssignedToDelivery`, доставленное позже, пропускается Record Courier Assignment.
- **Почему не fulfillment-факт:** отмену Delivery запрашивает сам Order Fulfillment workflow (компенсация `CancelDelivery`), снятие курьера возвращает Delivery в поиск; `DeliveryFailed` (после Pickup) обрабатывает Record Delivery Failure. Поэтому `Order` и workflow не меняются, `reason` отмены не используется.
- **Reconciliation:** `NULL` в `courier_id`/`delivery_id` — отсутствующая relation; остаток в SpiceDB удаляется как orphan после grace.
- **Порты:** `application.UnitOfWork`, `application.Inbox`, `recordcourierassignment.Assignments`, `Relationships`.
- **Inbox consumer:** `orders.recordcourierrelease`; повтор `event_id` даёт outcome `duplicate`.
- **Outcomes:** `released`, `ignored_stale`, `duplicate`. Ошибка SpiceDB откатывает Inbox record, и redelivery повторяет событие.
- **Outbox:** не пишется.
- **Требования:** FR-476, FR-477, AC-534…AC-538 в OpenSpec change `add-order-command-rpcs-and-relationships`.
- **`.feature`:** сценарии проверяются unit-тестами handler на in-memory адаптерах; Gherkin runner (`godog`) пока не подключён.

## Acceptance

Executable-сценарии: [`recordcourierrelease.feature`](acceptance/recordcourierrelease.feature).

Общий каталог: [Orders use cases](../../../../README.md#use-cases).

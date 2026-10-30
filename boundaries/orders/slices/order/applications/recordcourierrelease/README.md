# Record Courier Release

## Сценарий

- **Тип:** Command.
- **Actor:** Kafka consumer `orders`.
- **Намерение:** Отразить, что `delivery` сняла курьера без замены или отменила Delivery.
- **Результат:** курьер снят с сохранённого назначения; при отмене Delivery снята и Delivery, и записано `RelationshipsChanged` — Sync Order Relationships удаляет `order#delivery`. Доступ курьера к заказу отзывает `delivery` (`delivery#courier`), `orders` для этого SpiceDB не трогает.

## Границы

- **Владелец:** `orders/slices/order`.
- **Домен:** FSM `Order` не меняется: снятие курьера и отмена Delivery не являются её шагами. Снятие назначенного курьера сохраняет `Order`, поэтому `order_details` и Change Notification убирают `courier_assigned_at`.
- **Взаимодействие:** Принимает integration events `CourierUnassignedFromDelivery` и `DeliveryCancelled` от `delivery` через Transactional Inbox; workflow не уведомляется.

## Последовательность

```mermaid
sequenceDiagram
    participant Kafka
    participant Inbox as Transactional Inbox
    participant App as Use case
    participant Store as orders (назначение)
    Kafka->>Inbox: integration event
    Inbox->>Inbox: дедупликация
    Inbox->>App: снятие / отмена
    App->>Store: назначение FOR UPDATE
    App->>Store: courier_id = NULL [, delivery_id = NULL], время, версия
    App->>Store: Outbox RelationshipsChanged (только DeliveryCancelled)
    Inbox-->>Kafka: ack после commit
```

## Реализация

Handler: [`recordcourierrelease.go`](recordcourierrelease.go), unit-тесты: [`recordcourierrelease_test.go`](recordcourierrelease_test.go).

- **Поведение:** `Input{EventID, OrderID, OccurredAt, DeliveryVersion, DeliveryID, CourierID, DeliveryCancelled}`; `DeliveryVersion` — `aggregate_version` envelope. В одном unit of work (`application.AcceptOnce`, тот же протокол Inbox, что у `fulfillment.Reflector`): чтение назначения под row lock; устарело ли событие и какое назначение остаётся, решает домен (`order.CourierAssignment.Release`, `CourierRelease.Kept`); затем `CourierAssignments.RememberCourierAssignment` с пустым курьером (и Delivery при отмене), временем и версией события; при `DeliveryCancelled` сохранённой Delivery (`HoldsRelationsOf`; снятие курьера связи не меняет) — `syncorderrelationships.Changes.RelationshipsChanged` в той же транзакции ([ADR 18](../../../../../../docs/adr/0018-spicedb-for-authorization.md)).
- **Guard:** порядок задаёт версия Delivery (у Order одна Delivery, каждая транзакция Delivery увеличивает версию), а не `occurred_at` (`DEFAULT now()` — начало транзакции delivery). Событие другой Delivery или с версией ниже сохранённой — `ignored_stale` без изменений, но с Inbox record. Применённое событие снимает сохранённого курьера, даже если называет другого: после этой версии у Delivery нет курьера, а назначение названного старше и будет пропущено. Без сохранённого назначения ничего не удаляется, но снятие запоминается со своей версией (`remembered_ahead`), поэтому более старое `CourierAssignedToDelivery`, обработанное позже, пропускается Record Courier Assignment.
- **Почему не шаг FSM:** отмену Delivery запрашивает сам Order Fulfillment workflow (компенсация `CancelDelivery`), снятие курьера возвращает Delivery в поиск; `DeliveryFailed` (после Pickup) обрабатывает Record Delivery Failure. Поэтому `Order` и workflow не меняются, `reason` отмены не используется.
- **Связи:** `NULL` в `delivery_id` — отсутствующая `order#delivery`: Sync Order Relationships приводит SpiceDB к состоянию строки. `courier_id` — бизнес-данные, relation `courier` у `order` нет.
- **Порты:** `application.UnitOfWork`, `application.Inbox`, `order.CourierAssignments` (контракт хранения домена), `syncorderrelationships.Changes`.
- **Inbox consumer:** `orders.recordcourierrelease`; повтор `event_id` даёт outcome `duplicate`.
- **Outcomes:** `released`, `remembered_ahead`, `ignored_stale`, `duplicate`. Ошибка записи `RelationshipsChanged` откатывает Inbox record вместе со снятием, и redelivery повторяет событие; SpiceDB use case не вызывает.
- **Outbox:** только `RelationshipsChanged` во внутренний стрим `orders.v1.order-relationships`; Integration Events не пишутся.
- **Требования:** FR-476, AC-534…AC-537 (в редакции change `scope-courier-access-to-assignment`: снятие курьера SpiceDB не трогает, FR-477 удалён); FR-564, AC-669, AC-670 в change `harden-courier-relationship-ordering`.
- **`.feature`:** сценарии проверяются unit-тестами handler против PostgreSQL (testcontainers); Gherkin runner (`godog`) пока не подключён.

## Acceptance

Executable-сценарии: [`recordcourierrelease.feature`](acceptance/recordcourierrelease.feature).

Общий каталог: [Orders use cases](../../../../README.md#use-cases).

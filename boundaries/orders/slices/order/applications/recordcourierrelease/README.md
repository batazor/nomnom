# Record Courier Release

## Сценарий

- **Тип:** Command.
- **Actor:** Kafka consumer `orders`.
- **Намерение:** Отразить, что `delivery` сняла курьера без замены или отменила Delivery.
- **Результат:** курьер снят с сохранённого назначения; при отмене Delivery удалена `order#delivery`. Доступ курьера к заказу отзывает `delivery` (`delivery#courier`), `orders` для этого SpiceDB не трогает.

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
    App->>SpiceDB: DELETE order#delivery (только DeliveryCancelled)
    App->>Store: courier_id = NULL [, delivery_id = NULL], время, revision
    Inbox-->>Kafka: ack после commit
```

## Реализация

Handler: [`recordcourierrelease.go`](recordcourierrelease.go), unit-тесты: [`recordcourierrelease_test.go`](recordcourierrelease_test.go).

- **Поведение:** `Input{EventID, OrderID, OccurredAt, DeliveryVersion, DeliveryID, CourierID, DeliveryCancelled}`; `DeliveryVersion` — `aggregate_version` envelope. В одном unit of work (`application.AcceptOnce`, тот же протокол Inbox, что у `fulfillment.Reflector`): чтение назначения под row lock; устарело ли событие и какое назначение остаётся, решает домен (`order.CourierAssignment.Release`, `CourierRelease.Kept`); затем при `DeliveryCancelled` — `Relationships.UnlinkDelivery` сохранённой Delivery (`HoldsRelationsOf`; снятие курьера SpiceDB не трогает) и `CourierAssignments.RememberCourierAssignment` с пустым курьером (и Delivery при отмене), временем, версией события и ZedToken.
- **Guard:** порядок задаёт версия Delivery (у Order одна Delivery, каждая транзакция Delivery увеличивает версию), а не `occurred_at` (`DEFAULT now()` — начало транзакции delivery). Событие другой Delivery или с версией ниже сохранённой — `ignored_stale` без изменений, но с Inbox record. Применённое событие снимает сохранённого курьера, даже если называет другого: после этой версии у Delivery нет курьера, а назначение названного старше и будет пропущено. Без сохранённого назначения ничего не удаляется, но снятие запоминается со своей версией (`remembered_ahead`), поэтому более старое `CourierAssignedToDelivery`, обработанное позже, пропускается Record Courier Assignment.
- **Почему не fulfillment-факт:** отмену Delivery запрашивает сам Order Fulfillment workflow (компенсация `CancelDelivery`), снятие курьера возвращает Delivery в поиск; `DeliveryFailed` (после Pickup) обрабатывает Record Delivery Failure. Поэтому `Order` и workflow не меняются, `reason` отмены не используется.
- **Reconciliation:** `NULL` в `delivery_id` — отсутствующая `order#delivery`; остаток в SpiceDB удаляется как orphan после grace. `courier_id` — бизнес-данные, relation `courier` не сверяется.
- **Порты:** `application.UnitOfWork`, `application.Inbox`, `order.CourierAssignments` (контракт хранения домена), `Relationships`.
- **Inbox consumer:** `orders.recordcourierrelease`; повтор `event_id` даёт outcome `duplicate`.
- **Outcomes:** `released`, `remembered_ahead`, `ignored_stale`, `duplicate`. Ошибка SpiceDB откатывает Inbox record, и redelivery повторяет событие.
- **Outbox:** не пишется.
- **Требования:** FR-476, AC-534…AC-537 (в редакции change `scope-courier-access-to-assignment`: снятие курьера SpiceDB не трогает, FR-477 удалён); FR-564, AC-669, AC-670 в change `harden-courier-relationship-ordering`.
- **`.feature`:** сценарии проверяются unit-тестами handler на in-memory адаптерах; Gherkin runner (`godog`) пока не подключён.

## Acceptance

Executable-сценарии: [`recordcourierrelease.feature`](acceptance/recordcourierrelease.feature).

Общий каталог: [Orders use cases](../../../../README.md#use-cases).

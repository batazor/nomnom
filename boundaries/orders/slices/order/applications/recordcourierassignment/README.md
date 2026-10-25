# Record Courier Assignment

## Сценарий

- **Тип:** Command.
- **Actor:** Kafka consumer `orders`.
- **Намерение:** Отразить назначение курьера.
- **Результат:** Назначение отражено в `Order` не более одного раза.

## Границы

- **Владелец:** `orders/slices/order`.
- **Домен:** `domains/order`.
- **Взаимодействие:** Принимает integration event от `delivery` через Transactional Inbox и уведомляет fulfillment workflow.

## Последовательность

```mermaid
sequenceDiagram
    participant Kafka
    participant Inbox as Transactional Inbox
    participant App as Use case
    participant Order
    participant Workflow as Fulfillment workflow
    Kafka->>Inbox: integration event
    Inbox->>Inbox: дедупликация
    Inbox->>App: бизнес-результат
    App->>Order: применить допустимый переход
    App->>Workflow: передать бизнес-результат
    Inbox-->>Kafka: ack после commit
```

## Реализация

Handler: [`recordcourierassignment.go`](recordcourierassignment.go), unit-тесты: [`recordcourierassignment_test.go`](recordcourierassignment_test.go).

- **Поведение:** `Input{EventID, OrderID, OccurredAt, DeliveryID, CourierID}` → `Order.RecordCourierAssignment`: факт `courier_assigned` записывается один раз; Order State не меняется.
- **Relationships:** в том же Inbox unit of work (`fulfillment.Reflector.ReflectWith`) назначение `order.CourierAssignment{DeliveryID, CourierID, AssignedAt, DeliveryVersion}` читается под row lock; правила — в домене: событие с Delivery `aggregate_version` ниже сохранённого назначения или снятия пропускается (`Supersedes`, FR-564; `occurred_at` порядок commit не задаёт), иначе `order.CourierAssignments` сохраняет назначение и версию. В SpiceDB пишется только `order#delivery@delivery`: `Relationships.LinkDelivery` для новой Delivery одним запросом делает `TOUCH` её и `DELETE` вытесненной (`DisplacedDelivery`) и сохраняет ZedToken; переназначение курьера в пределах уже связанной Delivery (`LinksDelivery`) запросов к SpiceDB не делает и `authorization_revision` не меняет. Курьер видит заказ через `delivery->act_as_courier`, которым управляет `delivery`. Reassignment (`already_applied`) меняет только назначение, срок Pickup и, для другой Delivery, `order#delivery`. Срок Pickup (`ExpectedPickupAt`, FR-611) берёт только не устаревшее по `Supersedes` событие: `Order.ExpectPickupAt` заменяет прежний срок (пустой — не назван), и `Order` сохраняется, если срок изменился. Ошибка SpiceDB откатывает Inbox record. Событие без `delivery_id`/`courier_id` отражает факт без relationships.
- **Порты:** `application.UnitOfWork`, `application.Inbox`, `order.Repository`, `fulfillment.Notifier`, `order.CourierAssignments`, `Relationships`; общий поток — [`fulfillment.Reflector`](../fulfillment/README.md).
- **Inbox consumer:** `orders.recordcourierassignment`; повтор `event_id` даёт outcome `duplicate` и повторное уведомление workflow с тем же `event_id`.
- **Outcomes:** `applied`, `already_applied` (повтор факта под новым `event_id`), `ignored_late` (`Order` в `completed`/`cancelled`, `event_id` фиксируется), `duplicate`. Закрытый workflow не является ошибкой (`WorkflowNotified=false`); прочая ошибка уведомления возвращается, чтобы событие было доставлено повторно.
- **Outbox:** не пишется — исходящий бизнес-факт для этого сценария не определён.
- **Требования:** FR-104, FR-107, FR-109, FR-110; AC-106, AC-107, AC-110, AC-112…AC-114 в OpenSpec change `implement-orders-application-layer`; FR-469, AC-518…AC-521 в change `add-order-command-rpcs-and-relationships`, в редакции change `scope-courier-access-to-assignment` (только `order#delivery`).
- **`.feature`:** сценарии `@AC-002` проверяются unit-тестами handler против PostgreSQL (testcontainers); Gherkin runner (`godog`) пока не подключён.

## Acceptance

Executable-сценарии: [`recordcourierassignment.feature`](acceptance/recordcourierassignment.feature).

Общий каталог и место сценария в fulfillment process: [Orders use cases](../../../../README.md#use-cases).

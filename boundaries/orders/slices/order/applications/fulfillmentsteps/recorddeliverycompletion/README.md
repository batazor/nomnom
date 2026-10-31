# Record Delivery Completion

## Сценарий

- **Тип:** Command.
- **Actor:** Kafka consumer `orders`.
- **Намерение:** Отразить успешное завершение доставки.
- **Результат:** Delivery Completion отражён в `Order` не более одного раза.

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

Handler: [`recorddeliverycompletion.go`](recorddeliverycompletion.go), сценарии: [`acceptance_test.go`](acceptance_test.go), остальные случаи: [`inbox_test.go`](inbox_test.go).

- **Поведение:** `Input{EventID, OrderID, OccurredAt}` → `Order.RecordDeliveryCompletion`: переход в `delivered`; `Order` не завершается — Completion фиксирует `completeorder`.
- **Порты:** `application.UnitOfWork`, `application.Inbox`, `order.Repository`, `fulfillment.Notifier`; поток входящего сигнала — [`fulfillment`](../../fulfillment/README.md#поток-входящего-сигнала).
- **Inbox consumer:** `orders.recorddeliverycompletion`; повтор `event_id` даёт outcome `duplicate` и повторное уведомление workflow с тем же `event_id`.
- **Outcomes:** `applied`, `already_applied` (повтор шага под новым `event_id`), `ignored_late` (`Order` в `completed`/`cancelled`, `event_id` фиксируется), `duplicate`. Закрытый workflow не является ошибкой (`WorkflowNotified=false`); прочая ошибка уведомления возвращается, чтобы событие было доставлено повторно.
- **Outbox:** не пишется — исходящее Integration Event для этого сценария не определено.
- **Требования:** FR-104, FR-107, FR-109, FR-110; AC-106, AC-107, AC-110, AC-112…AC-114 в OpenSpec change `implement-orders-application-layer`.

## Acceptance

Executable-сценарии: [`recorddeliverycompletion.feature`](acceptance/recorddeliverycompletion.feature) исполняются `godog` под `go test` против PostgreSQL (testcontainers). Сбои уведомления, транзакции и конкурентные изменения (AC-113, AC-114, AC-130) остаются обычными тестами.

Общий каталог и место сценария в fulfillment process: [Orders use cases](../../../../../README.md#use-cases).

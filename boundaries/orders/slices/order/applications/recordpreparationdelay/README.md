# Record Preparation Delay

## Сценарий

- **Тип:** Command.
- **Actor:** Kafka consumer `orders`.
- **Намерение:** Отразить задержку Preparation, о которой сообщил `restaurants`.
- **Результат:** Новый срок готовности заменил прежний в `Order` (FR-611, AC-771); workflow продлил ожидание Preparation.

## Реализация

Handler: [`recordpreparationdelay.go`](recordpreparationdelay.go), сценарии: [`acceptance_test.go`](acceptance_test.go), остальные случаи: [`recordpreparationdelay_test.go`](recordpreparationdelay_test.go).

- **Поведение:** `Input{EventID, OrderID, OccurredAt, ExpectedReadyAt}` → `Order.ExpectReadyAt`; `ExpectedReadyAt` обязателен. Задержка — не шаг FSM: каждая заменяет срок, названный раньше (`RestaurantOrderAccepted` или прежней задержкой).
- **Порты и поток:** `application.UnitOfWork`, `application.Inbox`, `order.Repository`, `fulfillment.Notifier`; поток входящего сигнала — [`fulfillment`](../fulfillment/README.md#поток-входящего-сигнала), уведомление workflow несёт `ExpectedReadyAt`.
- **Inbox consumer:** `orders.recordpreparationdelay`.
- **Outcomes:** `applied`, `ignored_late` (`Order` в `completed`/`cancelled`), `duplicate`. Сохранение `Order` кладёт Order Change Notification для `orderChanged`.

## Acceptance

Executable-сценарии: [`recordpreparationdelay.feature`](acceptance/recordpreparationdelay.feature) исполняются `godog` под `go test` против PostgreSQL (testcontainers).

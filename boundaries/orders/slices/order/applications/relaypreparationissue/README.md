# Relay Preparation Issue

## Сценарий

- **Тип:** Command.
- **Actor:** Kafka consumer `orders`.
- **Намерение:** Передать fulfillment workflow неудачу Preparation.
- **Результат:** Workflow получил `PreparationFailed`; `Order` не меняется. `PreparationDelayed` меняет срок готовности `Order` и обрабатывается [Record Preparation Delay](../recordpreparationdelay/README.md).

## Реализация

Handler: [`relaypreparationissue.go`](relaypreparationissue.go), unit-тесты: [`relaypreparationissue_test.go`](relaypreparationissue_test.go).

- **Поведение:** `Input{EventID, OrderID, OccurredAt, Reason}` → `fulfillment.Notifier.Notify` с `preparation_failed`.
- **Inbox и UoW:** не используются — это вход orchestration, а не шаг FSM `Order`; локального state нет, workflow дедуплицирует по `event_id`.
- **Ошибки:** закрытый workflow — не ошибка (`WorkflowNotified=false`); прочая ошибка уведомления возвращается для повторной доставки.

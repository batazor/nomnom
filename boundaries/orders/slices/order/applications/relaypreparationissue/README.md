# Relay Preparation Issue

## Сценарий

- **Тип:** Command.
- **Actor:** Kafka consumer `orders`.
- **Намерение:** Передать fulfillment workflow задержку или неудачу Preparation.
- **Результат:** Workflow получил `PreparationDelayed` или `PreparationFailed`; `Order` не меняется.

## Реализация

Handler: [`relaypreparationissue.go`](relaypreparationissue.go), unit-тесты: [`relaypreparationissue_test.go`](relaypreparationissue_test.go).

- **Поведение:** `Input{EventID, OrderID, Kind, OccurredAt, Reason, ExpectedReadyAt}` → `fulfillment.Notifier.Notify`. `ExpectedReadyAt` обязателен для `preparation_delayed`.
- **Inbox и UoW:** не используются — это входы orchestration, а не Fulfillment Facts `Order`; локального state нет, workflow дедуплицирует по `event_id`.
- **Ошибки:** закрытый workflow — не ошибка (`WorkflowNotified=false`); прочая ошибка уведомления возвращается для повторной доставки.

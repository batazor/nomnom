# Spec Delta

## ADDED Requirements

### Requirement: Отказ Cancellation Request называет причину
**ID:** FR-494

Отказ Request Order Cancellation по окну отмены SHALL возвращаться gRPC `FAILED_PRECONDITION` с `ErrorInfo.reason` `CANCELLATION_WINDOW_NOT_OPEN` или `CANCELLATION_WINDOW_EXPIRED`; отказ по состоянию заказа или процессу SHALL сохранять `ORDER_STATE_CONFLICT`.

#### Scenario: Окно отмены истекло
**ID:** AC-562

- **WHEN** покупатель запрашивает отмену после закрытия окна
- **THEN** ответ — `FAILED_PRECONDITION` с `ErrorInfo.reason = CANCELLATION_WINDOW_EXPIRED`

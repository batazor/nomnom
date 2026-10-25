# List Payment Operations

## Сценарий

- **Тип:** Query.
- **Actor:** Авторизованный оператор или разрешённый пользовательский сценарий.
- **Намерение:** Получить историю операций по `Payment`.
- **Результат:** Возвращён разрешённый список попыток и результатов без изменения состояния.

## Границы

- **Владелец:** `payments/slices/payment`.
- **Домен:** `domains/payment`.
- **Взаимодействие:** Читает query model; диагностическое назначение не ослабляет authorization.

## Реализация

Handler `ListPaymentOperationsHandler` в [`handler.py`](handler.py), требования FR-411, FR-413 change [`implement-payments-application-layer`](../../../../../../openspec/changes/implement-payments-application-layer/). Возвращает операции в порядке `requested_at` без provider data; отдельная read model не вводится.

## Acceptance

BDD-сценарии размещаются в `acceptance/list_payment_operations.feature` после утверждения acceptance criteria в SDD.

Общий каталог: [Payments use cases](../../../../README.md#use-cases).


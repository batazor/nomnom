# Get Payment Status

## Сценарий

- **Тип:** Query.
- **Actor:** Fulfillment process `orders`, клиент или авторизованный оператор.
- **Намерение:** Получить текущее разрешённое представление оплаты.
- **Результат:** Возвращено состояние `Payment` без изменения данных.

## Границы

- **Владелец:** `payments/slices/payment`.
- **Домен:** `domains/payment`.
- **Взаимодействие:** Читает query model; чувствительные provider data не раскрываются.

## Реализация

Handler `GetPaymentStatusHandler` в [`handler.py`](handler.py), требования FR-411, FR-412 change [`implement-payments-application-layer`](../../../../../../openspec/changes/implement-payments-application-layer/). Читает read model `PaymentStatusReader` (адаптер `infrastructure/repository/queries.py`) прямо из таблицы `payments`, без восстановления агрегата `Payment` и без Unit of Work; результат не содержит `Provider Reference`.

## Acceptance

BDD-сценарии размещаются в `acceptance/get_payment_status.feature` после утверждения acceptance criteria в SDD.

Общий каталог: [Payments use cases](../../../../README.md#use-cases).


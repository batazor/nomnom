# Report Delivery Failure

## Сценарий

- **Тип:** Command.
- **Actor:** Назначенный курьер или авторизованный диспетчер.
- **Намерение:** Зафиксировать невозможность завершить доставку.
- **Результат:** Неудача и причина сохранены; результат доступен `orders`.

## Границы

- **Владелец:** `delivery/slices/delivery`.
- **Домен:** `domains/delivery`.
- **Взаимодействие:** Записывает бизнес-факт через Outbox; retryability причины определяется SDD.

## Последовательность

```mermaid
sequenceDiagram
    actor Actor as Курьер / диспетчер
    participant App as Use case
    participant Delivery
    participant Store as Unit of Work / Outbox
    participant Kafka
    Actor->>App: сообщить результат доставки
    App->>Delivery: проверить actor и выполнить переход
    App->>Store: сохранить состояние и бизнес-факт
    Store-->>App: commit
    Store->>Kafka: опубликовать через Outbox
    App-->>Actor: результат
```

## Реализация

- **Handler:** `mod.rs` — `Input`, `Output`, `Handler::execute(subject, input)`; требования FR-317.
- **Порты:** `DeliveryAuthorization` (`ReportDeliveryFailure` над `Courier(id)` для курьера или `Delivery(id)` для диспетчера), `UnitOfWork`, `DeliveryRepository`, `CourierRepository`, `DeliveryOutbox`.
- **Транзакция:** UoW: `Delivery` (`failed` с причиной) + освобождение `Courier` + Outbox (`DeliveryFailed`).
- **Допущение:** Причина не классифицируется как retryable — решение остаётся за `orders`; неудача фиксируется только для `assigned`/`pickedUp`, курьер освобождается.

## Acceptance

BDD-сценарии размещаются в `acceptance/report_delivery_failure.feature` после утверждения acceptance criteria в SDD.

Общий каталог: [Delivery use cases](../../../../README.md#use-cases).


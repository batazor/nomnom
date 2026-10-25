# Confirm Delivery

## Сценарий

- **Тип:** Command.
- **Actor:** Назначенный курьер.
- **Намерение:** Подтвердить передачу заказа клиенту.
- **Результат:** Для `Delivery` зафиксирована Delivery Completion.

## Границы

- **Владелец:** `delivery/slices/delivery`.
- **Домен:** `domains/delivery`.
- **Взаимодействие:** Проверяет назначенного курьера и записывает бизнес-факт через Outbox.

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

- **Handler:** `mod.rs` — `Input`, `Output`, `Handler::execute(subject, input)`; требования FR-316, FR-318.
- **Порты:** `DeliveryAuthorization` (`ConfirmDelivery` над `Courier(id)`), `UnitOfWork`, `DeliveryRepository`, `CourierAssignment` (порт слайса `courier`), `DeliveryOutbox`.
- **Транзакция:** UoW: `Delivery` + освобождение `Courier` (`release_assignment` с `Revoke::Keep`) + Outbox (`DeliveryCompleted`); повтор назначенным курьером — no-op.
- **Relationship:** `delivery#courier` не отзывается: `Delivery.courier_id` сохраняется, а связь следует за ним (FR-444); закрытая доставка запрещает дальнейшие действия курьера. Revision не меняется (FR-447 касается только записей relationship).

## Acceptance

BDD-сценарии размещаются в `acceptance/confirm_delivery.feature` после утверждения acceptance criteria в SDD.

Общий каталог: [Delivery use cases](../../../../README.md#use-cases).


# Confirm Pickup

## Сценарий

- **Тип:** Command.
- **Actor:** Назначенный курьер.
- **Намерение:** Подтвердить получение заказа у ресторана.
- **Результат:** Для `Delivery` зафиксирован Pickup.

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

- **Handler:** `mod.rs` — `Input`, `Output`, `Handler::execute(subject, input)`; требования FR-314, FR-315.
- **Порты:** `DeliveryAuthorization` (`ConfirmPickup` над `Courier(id)`), `UnitOfWork`, `DeliveryRepository`, `DeliveryOutbox`, `AssignmentProcess` (после commit).
- **Транзакция:** UoW: `Delivery` + Outbox (`DeliveryPickedUp`); повтор назначенным курьером — no-op.

## Acceptance

BDD-сценарии размещаются в `acceptance/confirm_pickup.feature` после утверждения acceptance criteria в SDD.

Общий каталог: [Delivery use cases](../../../../../README.md#use-cases).


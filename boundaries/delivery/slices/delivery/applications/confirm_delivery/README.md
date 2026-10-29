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
- **Порты:** `DeliveryAuthorization` (`ConfirmDelivery` над `Courier(id)`), `UnitOfWork`, `DeliveryRepository`, `CourierAssignment` (порт слайса `courier`), `DeliveryOutbox`, `CandidateWake` (порт слайса `courier`, после commit).
- **Порты (дополнительно):** `DeliveryRelationshipWriter`.
- **Транзакция:** UoW: `Delivery` + освобождение `Courier` и DELETE `delivery#courier` до commit (`release_assignment`) + Outbox (`DeliveryCompleted`); повтор назначенным курьером — no-op без записи.
- **Relationship:** назначение закончилось, поэтому курьер теряет `delivery#act_as_courier` и `order#view` (AC-690); `Delivery.courier_id` сохраняется как факт. Revision `DELETE` сохраняется с доставкой (FR-447). Неудачный `DELETE` откатывает UoW: доставка остаётся picked up, курьер не освобождён, Outbox пуст (AC-692).

## Acceptance

BDD-сценарии размещаются в `acceptance/confirm_delivery.feature` после утверждения acceptance criteria в SDD.

Общий каталог: [Delivery use cases](../../../../README.md#use-cases).


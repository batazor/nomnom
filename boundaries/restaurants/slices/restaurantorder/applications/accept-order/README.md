# Accept Order

## Сценарий

- **Тип:** Command.
- **Actor:** Авторизованный сотрудник ресторана.
- **Намерение:** Подтвердить возможность приготовить весь `Kitchen Order`.
- **Результат:** `Kitchen Order` принят; результат доступен `orders`.

## Границы

- **Владелец:** `restaurants/slices/restaurantorder`.
- **Домен:** `domains/restaurantorder`.
- **Взаимодействие:** Проверяет открытость ресторана и доступность позиций, затем записывает бизнес-факт в Outbox.

## Последовательность

```mermaid
sequenceDiagram
    actor Staff as Сотрудник ресторана
    participant App as Use case
    participant Kitchen as Kitchen Order
    participant Store as Unit of Work / Outbox
    participant Kafka
    Staff->>App: изменить состояние заказа
    App->>Kitchen: выполнить доменный переход
    App->>Store: сохранить состояние и бизнес-факт
    Store-->>App: commit
    Store->>Kafka: опубликовать через Outbox
    App-->>Staff: результат
```

## Acceptance

BDD-сценарии размещаются в `acceptance/accept-order.feature` после утверждения acceptance criteria в SDD.

Общий каталог: [Restaurants use cases](../../../../README.md#use-cases).


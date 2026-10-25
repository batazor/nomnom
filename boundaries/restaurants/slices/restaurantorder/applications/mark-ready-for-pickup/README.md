# Mark Ready for Pickup

## Сценарий

- **Тип:** Command.
- **Actor:** Авторизованный сотрудник ресторана.
- **Намерение:** Сообщить о готовности заказа к выдаче.
- **Результат:** Preparation завершена; `Kitchen Order` готов к Pickup.

## Границы

- **Владелец:** `restaurants/slices/restaurantorder`.
- **Домен:** `domains/restaurantorder`.
- **Взаимодействие:** Записывает статусный бизнес-факт через Transactional Outbox.

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

BDD-сценарии размещаются в `acceptance/mark-ready-for-pickup.feature` после утверждения acceptance criteria в SDD.

Общий каталог: [Restaurants use cases](../../../../README.md#use-cases).


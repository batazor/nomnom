# Replan Delivery

## Сценарий

- **Тип:** Command.
- **Actor:** Fulfillment process `orders` (Temporal Activity, service identity `orders-worker`).
- **Намерение:** Перепланировать доставку заказа: снять неактуального курьера и предложить доставку новому.
- **Результат:** Доставка в поиске; подходящий курьер получил `Delivery Offer` либо кандидата нет.

## Границы

- **Владелец:** `delivery/slices/delivery`.
- **Домен:** `domains/delivery`.
- **Взаимодействие:** gRPC `ReplanDelivery`, адресуется `order_id`. Алгоритм выбора остаётся в `delivery`: use case компонует прикладные сервисы Reassign Courier, Find Courier и Offer Delivery без их авторизации.

## Последовательность

```mermaid
sequenceDiagram
    participant Workflow as orders Activity
    participant App as Replan Delivery
    participant Reassign as end_assignment
    participant Find as select_candidate
    participant Offer as make_offer
    Workflow->>App: ReplanDelivery(order_id)
    App->>App: проверить ReplanDelivery, открыть UoW
    App->>App: найти Delivery по order_id
    opt курьер назначен
        App->>Reassign: снять назначение, освободить курьера
    end
    App->>Find: подобрать кандидата
    alt кандидат найден
        App->>Offer: предложить доставку кандидату
        App->>App: commit
        App-->>Workflow: offered_courier_id
    else кандидата нет
        App->>App: commit
        App-->>Workflow: успех без offer
    end
```

## Реализация

- **Handler:** `mod.rs` — `Input { order_id }`, `Output { delivery_id, released_courier_id, offer }`.
- **Сервисы:** `reassign_courier::end_assignment`, `find_courier::select_candidate`, `offer_delivery::make_offer` — те же, что вызывают handlers Reassign Courier, Find Courier и Offer Delivery после собственной авторизации.
- **Порты:** `DeliveryAuthorization` (одна проверка `ReplanDelivery` над `Order(order_id)`, AC-633), `UnitOfWork`, `DeliveryRepository`, `CourierAssignment` (порт слайса `courier`), `DeliveryOfferRepository`, `OffersOfDelivery` (id offers доставки, каждый загружается через репозиторий), `DeliveryOutbox`, `CourierLocator`, `IdGenerator`, `Clock`, `AssignmentPolicy`, `DeliveryRelationshipWriter`.
- **Транзакция:** снятие назначения, выбор кандидата и offer — одна UoW (AC-633): ошибка поиска или offer откатывает снятие. DELETE relationship в SpiceDB не транзакционен: после отката недостающую связь восстанавливает reconciliation (FR-445). Offer идемпотентен по открытому offer той же пары.
- **Ошибки:** после Pickup или в финальном состоянии — `DeliveryPrecondition` (`FAILED_PRECONDITION`); неизвестный `order_id` — `DeliveryForOrderNotFound`.
- **Допущение:** курьеры, уже получавшие offer этой доставки, повторно не выбираются (правило Find Courier); поэтому повтор Replan может вернуть пустой `offered_courier_id`.

## Acceptance

BDD-сценарии размещаются в `acceptance/replan_delivery.feature` после утверждения acceptance criteria в SDD.

Общий каталог: [Delivery use cases](../../../../README.md#use-cases).

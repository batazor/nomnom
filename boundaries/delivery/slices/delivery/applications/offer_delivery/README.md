# Offer Delivery

## Сценарий

- **Тип:** Command.
- **Actor:** Процесс назначения `delivery`.
- **Намерение:** Предложить конкретному курьеру принять доставку.
- **Результат:** Создан актуальный ограниченный по времени `Delivery Offer`; assignment ещё не создан.

## Границы

- **Владелец:** `delivery/slices/delivery`.
- **Домен:** `domains/delivery`.
- **Взаимодействие:** Использует кандидата, выбранного Find Courier, и доменную policy допустимости offer. Внутренний Domain Event `DeliveryOffered` пишется в Outbox, не публикуется и метрикой не считается.

## Реализация

- **Handler:** `mod.rs` — `Input`, `Output`, `Handler::execute(subject, input)`; требования FR-308. Без авторизации шаг выполняет сервис `make_offer` (порты — `OfferPorts`), его же вызывают Replan Delivery и Dispatch Deliveries внутри своей UoW.
- **Порты:** `DeliveryAuthorization` (`OfferDelivery` над `Delivery(id)`), `UnitOfWork`, `DeliveryRepository`, `CourierAssignment` (порт слайса `courier`), `DeliveryOfferRepository`, `OffersOfDelivery` (id offers доставки, каждый загружается через репозиторий), `DeliveryOutbox`, `IdGenerator`, `Clock`, `AssignmentPolicy`.
- **Транзакция:** UoW: offer + Outbox (`DeliveryOffered`); открытый offer той же пары возвращается вместо нового и ничего не пишет.
- **Допущение:** Срок offer — `AssignmentPolicy::offer_ttl_seconds`; несколько курьеров могут одновременно иметь pending offers одной доставки.

## Acceptance

BDD-сценарии размещаются в `acceptance/offer_delivery.feature` после утверждения acceptance criteria в SDD.

Общий каталог: [Delivery use cases](../../../../README.md#use-cases).


# Offer Delivery

## Сценарий

- **Тип:** Command.
- **Actor:** Процесс назначения `delivery`.
- **Намерение:** Предложить конкретному курьеру принять доставку.
- **Результат:** Создан актуальный ограниченный по времени `Delivery Offer`; assignment ещё не создан.

## Границы

- **Владелец:** `delivery/slices/delivery`.
- **Домен:** `domains/delivery`.
- **Взаимодействие:** Использует кандидата, выбранного Find Courier, и доменную policy допустимости offer.

## Реализация

- **Handler:** `mod.rs` — `Input`, `Output`, `Handler::execute(subject, input)`; требования FR-308.
- **Порты:** `DeliveryAuthorization` (`OfferDelivery` над `Delivery(id)`), `DeliveryRepository`, `CourierRepository`, `DeliveryOfferRepository`, `IdGenerator`, `Clock`, `AssignmentPolicy`.
- **Транзакция:** Без UoW: одиночная вставка offer; открытый offer той же пары возвращается вместо нового.
- **Допущение:** Срок offer — `AssignmentPolicy::offer_ttl_seconds`; несколько курьеров могут одновременно иметь pending offers одной доставки.

## Acceptance

BDD-сценарии размещаются в `acceptance/offer_delivery.feature` после утверждения acceptance criteria в SDD.

Общий каталог: [Delivery use cases](../../../../README.md#use-cases).


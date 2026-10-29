# Reject Delivery Offer

## Сценарий

- **Тип:** Command.
- **Actor:** Курьер.
- **Намерение:** Отклонить актуальное предложение.
- **Результат:** Offer закрыт как отклонённый и не создаёт `Courier Assignment`.

## Границы

- **Владелец:** `delivery/slices/delivery`.
- **Домен:** `domains/delivery`.
- **Взаимодействие:** После commit, и на повторе, use case будит Assignment Workflow доставки; отклонённый offer больше не pending, поэтому workflow предлагает доставку другому курьеру.

## Реализация

- **Handler:** `mod.rs` — `Input`, `Output`, `Handler::execute(subject, input)`; требования FR-311.
- **Порты:** `DeliveryAuthorization` (`RejectDeliveryOffer` над `Courier(id)`), `DeliveryOfferRepository`, `Clock`, `AssignmentProcess` (после commit).
- **Транзакция:** Без UoW: одиночная запись; повтор отклонения — no-op.
- **Допущение:** Сам use case Find Courier не вызывает: следующий offer делает Assignment Workflow (FR-311).

## Acceptance

BDD-сценарии размещаются в `acceptance/reject_delivery_offer.feature` после утверждения acceptance criteria в SDD.

Общий каталог: [Delivery use cases](../../../../README.md#use-cases).


# Reject Delivery Offer

## Сценарий

- **Тип:** Command.
- **Actor:** Курьер.
- **Намерение:** Отклонить актуальное предложение.
- **Результат:** Offer закрыт как отклонённый и не создаёт `Courier Assignment`.

## Границы

- **Владелец:** `delivery/slices/delivery`.
- **Домен:** `domains/delivery`.
- **Взаимодействие:** Может инициировать новый поиск внутри `delivery`.

## Реализация

- **Handler:** `mod.rs` — `Input`, `Output`, `Handler::execute(subject, input)`; требования FR-311.
- **Порты:** `DeliveryAuthorization` (`RejectDeliveryOffer` над `Courier(id)`), `DeliveryOfferRepository`, `Clock`.
- **Транзакция:** Без UoW: одиночная запись; повтор отклонения — no-op.
- **Допущение:** Новый поиск автоматически не запускается.

## Acceptance

BDD-сценарии размещаются в `acceptance/reject_delivery_offer.feature` после утверждения acceptance criteria в SDD.

Общий каталог: [Delivery use cases](../../../../README.md#use-cases).


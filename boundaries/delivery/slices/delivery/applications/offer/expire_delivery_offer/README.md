# Expire Delivery Offer

## Сценарий

- **Тип:** Command.
- **Actor:** Таймер Assignment Workflow доставки (`delivery-worker`).
- **Намерение:** Закрыть pending offer, на который не ответили до `expiresAt`.
- **Результат:** Offer переведён в `expired`, `DeliveryOfferExpired` в Outbox; принятый, отклонённый, отозванный или ещё не просроченный offer не меняется.

## Границы

- **Владелец:** `delivery/slices/delivery`.
- **Домен:** `domains/delivery` (переход `Expire` offer).
- **Взаимодействие:** Вызывается Activity `expire` по Temporal timer на `expires_at + 1 s`; следующий offer делает Advance Assignment.

## Реализация

- **Handler:** `mod.rs` — `Input`, `Output`, `Handler::execute(input)`; требования FR-705.
- **Порты:** `DeliveryOfferRepository`, `DeliveryOutbox`, `Clock`.
- **Транзакция:** Одна UoW: offer и его факт в Outbox.
- **Допущение:** Без subject и проверки прав — работа процесса. `DeliveryOfferExpired` не публикуется, считается в `nomnom.delivery.offers{nomnom.outcome="expired"}`.

## Acceptance

BDD-сценарии размещаются в `acceptance/expire_delivery_offer.feature` после утверждения acceptance criteria в SDD.

Общий каталог: [Delivery use cases](../../../../../README.md#use-cases).

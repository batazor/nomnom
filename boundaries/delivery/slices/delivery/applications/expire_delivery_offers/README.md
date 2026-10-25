# Expire Delivery Offers

## Сценарий

- **Тип:** Command (фоновая задача).
- **Actor:** Процесс `delivery` по расписанию (`OfferExpiryWorker`).
- **Намерение:** Закрыть pending offers, на которые адресат не ответил до `expiresAt`.
- **Результат:** Offers с `expiresAt < now` переведены в `expired`; `Courier Assignment` не создаётся, повторный поиск не запускается.

## Границы

- **Владелец:** `delivery/slices/delivery`.
- **Домен:** `domains/delivery` — переход `Expire` агрегата `DeliveryOffer` (только из `pending` и только при `now > expiresAt`).
- **Взаимодействие:** Внутренний Domain Event `DeliveryOfferExpired` пишется в Outbox, не публикуется, считается метрикой `nomnom.delivery.offers{nomnom.outcome="expired"}`.

## Последовательность

```mermaid
sequenceDiagram
    participant Worker as OfferExpiryWorker
    participant App as Expire Delivery Offers
    participant Repo as DeliveryOfferRepository
    participant Offer as Delivery Offer
    participant Store as Unit of Work / Outbox
    Worker->>App: execute(batch_size)
    App->>Repo: list_expired_pending(now, batch_size)
    loop каждый offer пачки
        App->>Offer: expire(now)
        App->>Store: save offer + DeliveryOfferExpired
        Store-->>App: commit или конфликт версии (пропуск)
    end
    App-->>Worker: expired, skipped, more
```

## Реализация

- **Handler:** `mod.rs` — `Input { batch_size }`, `Output { expired, skipped, more }`, `Handler::execute(input)`; subject и проверки прав нет — задачу запускает сам процесс.
- **Порты:** `UnitOfWork`, `DeliveryOfferRepository` (`list_expired_pending`: pending, `expires_at < now`, по `expires_at`, не больше `limit`), `DeliveryOutbox`, `Clock`.
- **Транзакция:** UoW на каждый offer: offer (`expired`) + Outbox (`DeliveryOfferExpired`). Optimistic-concurrency конфликт — offer конкурентно принят, отклонён или отозван — пропуск, не ошибка; повторный запуск идемпотентен (уже `expired` offers не выбираются).
- **Расписание:** `OfferExpiryWorker` (`di/offer_expiry_worker.rs`) каждые `OFFERS__EXPIRY_INTERVAL` (по умолчанию `15s`) читает пачки по `OFFERS__EXPIRY_BATCH` (по умолчанию `100`), пока `more`; останавливается по сигналу shutdown основного worker.
- **Допущение:** Повторное предложение другому курьеру не делается: доставку перепланирует `orders` через `ReplanDelivery` по своему deadline назначения.

## Acceptance

BDD-сценарии размещаются в `acceptance/expire_delivery_offers.feature` после утверждения acceptance criteria в SDD.

Общий каталог: [Delivery use cases](../../../../README.md#use-cases).

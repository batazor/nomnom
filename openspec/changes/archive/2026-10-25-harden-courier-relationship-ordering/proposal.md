# Proposal

## Why

Ревью нашло четыре расхождения между реализацией и гарантиями авторизации и Outbox:

1. Reconciler восстанавливает missing relationship сразу по business state, который может быть прочитан до commit writer'а. Record Courier Assignment/Release в `orders` и Reassign Courier/Cancel Delivery в `delivery` делают `DELETE` прежнего курьера в SpiceDB до commit. Проход, прочитавший граф после `DELETE`, а PostgreSQL до commit, видит прежнего курьера «потерянным» и `TOUCH`-ит его обратно: снятый курьер видит заказ или доставку до истечения orphan grace.
2. Guards порядка courier events сравнивают `occurred_at`, а это `delivery_outbox.occurred_at DEFAULT now()` — начало транзакции delivery, не порядок commit. Envelope уже несёт `aggregate_version` Delivery, но `orders` его не использует. Снятие без сохранённого назначения ничего не запоминает, поэтому обработанное позже более старое `CourierAssignedToDelivery` выдаёт доступ.
3. Два конкурентных Place Order с одним idempotency key: второй получает `ErrIdempotencyKeyTaken`, который transport превращает в `INTERNAL`, хотя Order первого уже зафиксирован.
4. `delivery-relay` проверял advisory lock только между циклами, а цикл читает полные пачки подряд (FR-537 в change `publish-restaurant-and-menu-events` уже требует проверки перед каждой записью).

## What Changes

- **`orders` reconciler:** missing relationship восстанавливается сразу, но только после перечитывания subject под `FOR SHARE` lock строки заказа; `TOUCH` и `authorization_revision` пишутся в той же транзакции. Writers держат `FOR UPDATE` от чтения назначения до commit, поэтому перечитывание ждёт их commit. Счётчик `missing_superseded`.
- **`delivery` reconciler:** writers пишут SpiceDB до `UPDATE` без row lock, поэтому missing восстанавливается по тому же двухпроходному правилу с grace, что и удаление orphan. Счётчик `missing_pending`.
- **Порядок courier events — Delivery `aggregate_version`:** миграция `orders` `00004` добавляет `courier_delivery_version`; Record Courier Assignment и Release сравнивают версии. Применённое снятие освобождает сохранённого курьера; снятие без сохранённого назначения запоминается со своей версией (outcome `remembered_ahead`).
- **Place Order:** при `ErrIdempotencyKeyTaken` транзакция откатывается, key перечитывается после commit конкурента, и запрос возвращает его Order (`replayed`); иначе `ErrConcurrencyConflict` (`ABORTED`).
- **`delivery-relay`:** сессия lock проверяется (`ping`) перед каждой записью; потеря lock останавливает пачку до следующей записи и пропускает retention (реализация FR-537 из `publish-restaurant-and-menu-events`, delta здесь не повторяется).

## Capabilities

### New Capabilities

Нет.

### Modified Capabilities

- `order-relationship-reconciliation`: правило восстановления missing в `orders`.
- `restaurant-delivery-relationships`: двухпроходное восстановление missing в `delivery`.
- `spicedb-authorization`: guards по Delivery `aggregate_version`, запоминание снятия.
- `order-use-cases`: конкурентный повтор Place Order.

## Impact

`boundaries/orders` (reconcile, postgres store и миграция `00004`, Record Courier Assignment/Release, messaging mapper, Place Order, тесты, README), `boundaries/delivery` (`infrastructure/reconcile.rs`, `infrastructure/outbox`, тесты, README), `docs/security.md`. Restaurants reconciler (`restaurant#staff`, Revoke Staff до commit) имеет тот же риск и в этот change не входит.

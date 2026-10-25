# Proposal

## Why

Пользовательские права `restaurants` (`restaurant#owner`, `restaurant#staff`) и `delivery` (`courier#self`, `delivery#courier`) проверяются в SpiceDB, но relationships никто не пишет, поэтому владелец, персонал кухни и курьеры получают отказ. Нет и сценария назначения персонала ресторана.

## What Changes

- `restaurants`: SpiceDB-реализация `OwnerRelationshipWriter` (`TOUCH restaurant#owner` при регистрации); use cases Assign Staff / Revoke Staff (permission `manage`), состав персонала в агрегате `Restaurant`, `TOUCH`/`DELETE restaurant#staff`.
- `delivery`: `TOUCH courier#self@user` при регистрации курьера (`courier_id` = `user_id`); `delivery#courier` следует `Delivery.courier_id`: `TOUCH` при Assignment, `DELETE` при Reassign и Cancel.
- Reconciliation этих relations в каждом контексте по образцу `orders`: двухфазное удаление orphan/stale после grace, восстановление missing; до постоянного хранилища — фоновый цикл в основном процессе.
- `authorization/bootstrap.yaml`: assertions для owner/staff/courier paths.

## Capabilities

### New Capabilities

- `restaurant-delivery-relationships`: запись и сверка relationships `restaurant`, `courier`, `delivery`.

### Modified Capabilities

Нет.

## Impact

`boundaries/restaurants` (домен `Restaurant`, use cases, SpiceDB writer, reconciler), `boundaries/delivery` (use cases регистрации, Assignment, Reassign, Cancel; SpiceDB writer, reconciler), `authorization/bootstrap.yaml`, `docs/security.md`.

# Proposal

## Why

`restaurants` и `delivery` проверяют права в SpiceDB с `minimize_latency`, поэтому сразу после записи relationship (регистрация ресторана, назначение персонала, назначение курьера) проверка несколько секунд может отказывать, а после отзыва (Revoke Staff, Reassign Courier) — ещё разрешать. `orders` решает это сохранённым ZedToken ([security](../../../docs/security.md#consistency-и-zedtoken)).

## What Changes

- Writers relationships `restaurants` и `delivery` возвращают ZedToken записи (`TOUCH` и `DELETE`).
- Authorization revision сохраняется вместе с ресурсом: `Restaurant` (owner, staff), `Courier` (`self`), `Delivery` (`courier`), в той же операции сохранения.
- SpiceDB-адаптеры проверяют `restaurant:<id>`, `courier:<id>`, `delivery:<id>` с `at_least_as_fresh` по сохранённой revision; без revision и для `platform:nomnom` — `minimize_latency`.
- Reconciler при восстановлении missing relationship обновляет revision.

## Capabilities

### New Capabilities

- `authorization-revisions`: свежесть проверок прав после собственных записей relationships в `restaurants` и `delivery`.

### Modified Capabilities

Нет.

## Impact

Домены `Restaurant`, `Courier`, `Delivery` (техническое поле revision в snapshot), writers, SpiceDB-адаптеры, reconcilers, тесты обоих контекстов.

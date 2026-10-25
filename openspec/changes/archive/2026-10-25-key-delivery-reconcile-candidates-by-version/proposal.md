# Proposal

## Why

Reconciler `delivery` восстанавливает missing `courier#self`/`delivery#courier`, только если relationship отсутствовала при том же subject на двух проходах с интервалом не меньше grace (FR-563). Правило сравнивает только пару resource–subject. Если между проходами курьера сняли (commit), снова назначили и снова сняли, а второй Reassign Courier уже сделал `DELETE`, но ещё не закоммитил `UPDATE` строки, второй проход видит того же курьера назначенным без связи и восстанавливает только что снятый доступ.

## What Changes

- **Reconciler `delivery`:** missing-кандидат привязан к версии строки `Courier` (для `courier#self`) или `Delivery` (для `delivery#courier`). Страницы `ReconciliationStore::courier_page`/`assignment_page` возвращают `version`. Восстановление — только если оба прохода видели ту же версию; другая версия означает commit writer'а между проходами, и grace начинается заново (кандидат перезаписывается с новым временем и версией, `missing_pending`).
- Кандидат, ставший present или потерявший subject, по-прежнему забывается.

## Capabilities

### New Capabilities

Нет.

### Modified Capabilities

- `restaurant-delivery-relationships`: восстановление missing relationship в `delivery` учитывает версию строки Courier/Delivery (FR-563).

## Impact

`boundaries/delivery` (`infrastructure/reconcile.rs`, `infrastructure/reconcile/sources.rs`, PostgreSQL и memory `ReconciliationStore`, unit-тесты `infrastructure/reconcile/tests.rs`), README `delivery`, `docs/security.md`. Реализовано в commit `2ba81f8`.

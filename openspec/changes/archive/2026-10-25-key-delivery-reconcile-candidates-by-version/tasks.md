# Tasks

## 1. Reconciliation

- [x] 1.1 `ReconciliationStore::courier_page` и `assignment_page` возвращают версию строки `Courier`/`Delivery` (PostgreSQL и memory store) (`FR-563`).
- [x] 1.2 Missing-кандидат хранит время первого наблюдения и версию; другая версия на следующем проходе начинает grace заново (`FR-563`, `AC-760`); unit-тест `reassigned_and_revoked_between_passes_is_not_restored` в `infrastructure/reconcile/tests.rs`.

## 2. Документация и проверка

- [x] 2.1 README `delivery` (reconciliation, relay, tracing, пулер PostgreSQL), `docs/security.md`.
- [x] 2.2 `cargo fmt --check`, `cargo clippy`, `cargo test`; `openspec validate key-delivery-reconcile-candidates-by-version --strict`.

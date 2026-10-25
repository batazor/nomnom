# Tasks

## 1. Reconciliation

- [x] 1.1 `orders`: `Store.RestoreRelation` (перечитывание под `FOR SHARE`, `TOUCH` и revision в одной транзакции), reconciler и счётчик `missing_superseded` (`FR-566`, `AC-667`); integration-тесты `TestReconcileDoesNotResurrectReplacedCourier`, `TestRestoreRelation`.
- [x] 1.2 `delivery`: missing восстанавливается после двух проходов с интервалом ≥ grace, `missing_pending` (`FR-563`, `AC-668`); unit-тесты `infrastructure/reconcile/tests.rs`, `tests/spicedb_relationships.rs`.

## 2. Порядок courier events

- [x] 2.1 Миграция `00004_courier_delivery_version.sql`; `Assignment.DeliveryVersion`, mapper `aggregate_version` → `DeliveryVersion` (`FR-564`).
- [x] 2.2 Record Courier Assignment и Release сравнивают версии; снятие освобождает сохранённого курьера и запоминается без сохранённого назначения (`FR-469`, `FR-476`, `AC-520`, `AC-536`, `AC-669`, `AC-670`); unit-тесты обоих use cases и Watermill handler, integration-тесты store.

## 3. Place Order

- [x] 3.1 `placeorder.ErrIdempotencyKeyTaken`, replay после конфликта, `ErrConcurrencyConflict` без Order (`FR-565`, `AC-671`); unit-тесты, integration-тест `TestIdempotencyKeyConcurrentInsert`.

## 4. Relay

- [x] 4.1 `OutboxStore::holds_lock`, `PgOutbox::lock_alive`; `relay_batch` проверяет lock перед каждой записью, цикл при потере lock не выполняет retention (`FR-537`, `AC-615`); unit-тесты relay, integration-тест `tests/outbox_relay_lock.rs`.

## 5. Документация и проверка

- [x] 5.1 `docs/security.md`, README `orders`, `delivery`, use cases.
- [x] 5.2 `orders`: `go vet`, `make lint`, `go test ./...`, integration (PostgreSQL, SpiceDB, Kafka); `delivery`: `cargo fmt --check`, `cargo clippy`, `cargo test`, env-gated тесты; `openspec validate harden-courier-relationship-ordering --strict`.

# Tasks

## 1. Reconciliation

- [x] 1.1 `RelationshipReconciler`: missing `restaurant#owner`/`restaurant#staff` восстанавливается после двух проходов с интервалом ≥ grace, кандидат забывается, когда перестал быть missing; `ReconcileStats.missingPending`, `missing_pending` в log (`FR-445`, `AC-700`, `AC-701`).
- [x] 1.2 Unit-тесты `test/reconcile.test.ts` (Revoke Staff в полёте с отложенным save, двухпроходное восстановление, забывание кандидата); env-gated integration-тест `test/reconcile-mongodb-integration.test.ts` (MongoDB replica set и SpiceDB); обновлён AC-478 в `test/spicedb-relationships-integration.test.ts`.

## 2. Документация и проверка

- [x] 2.1 README `restaurants` (reconciliation, `RECONCILER__GRACE`), README Revoke Staff, `docs/security.md`.
- [x] 2.2 `npm run check`, `npm run build`, `npm test`, integration-тесты против MongoDB и SpiceDB; `openspec validate harden-restaurant-relationship-reconciliation --strict`.

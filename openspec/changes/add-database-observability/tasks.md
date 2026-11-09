# Tasks

## 1. Текст запроса в spans

- [x] 1.1 `orders`: otelpgx без `WithDisableSQLStatementInAttributes`; unit-тест — `db.query.text` и `db.operation.name` есть, значений параметров нет; `db.namespace` otelpgx берёт из соединения, проверяется в Tempo (`FR-769`, `AC-1083`).
- [x] 1.2 `delivery`: текст литерала в `Statement`, `db.query.text` и `db.namespace` (имя базы из URI) в client span, `sql!` передаёт namespace store; тесты (`FR-769`, `AC-1084`).

## 2. PostgreSQL и Alloy в kind

- [x] 2.1 `ops/kind/bootstrap`: параметры `pg_stat_statements` в args сервера, расширение в `postgres`, `pg_read_all_data` и `pg_stat_statements.track = 'none'` для `alloy`, `pg_hba` для `postgres`, Secret `alloy-postgres` с `POSTGRES_DSN` (`FR-770`).
- [x] 2.2 Alloy v1.21.3: `database_observability.postgres` вместо трёх `prometheus.exporter.postgres`, relabel `owner`/`instance`, allowlist, `loki.write` логов компонента; `GRAFANA_CLOUD_LOGS_*` в `.env.grafana-cloud.example`, kustomization и `require-grafana-cloud` (`FR-771`, `AC-1087`).

## 3. Dashboards и alerts

- [x] 3.1 `ops/grafana/overview`: job `integrations/db-o11y`, `pg_up` по `instance`; инвентарь метрик (`pg_stat_statements_*`, `database_observability_connection_info`); `make grafana-generate` (`FR-772`).

## 4. Документация

- [x] 4.1 `docs/observability.md` (текст statement, Database Observability), ADR 9, README `orders` и `delivery`, `ops/grafana-cloud/README.md`, `docs/runbooks.md`.

## 5. Проверка

- [ ] 5.1 Тесты `orders`, `delivery`, `ops/grafana`; `make kind-deploy GRAFANA_CLOUD=1`; `pg_stat_statements` от роли `alloy` (`AC-1085`); записи компонента в Loki (`AC-1086`); Platform / Overview (`AC-1088`); переход span → запрос (`AC-1089`).

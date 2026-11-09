# Proposal

## Why

Client span PostgreSQL в Tempo показывает только операцию и таблицу. Медленный span нельзя связать с конкретным запросом, его планом и историей выполнений. Grafana Cloud Database Observability умеет это по trace linkage: на span с текстом запроса появляется переход к собранным Alloy данным запроса — samples, wait events, explain plan, схема. Без изменений в приложениях связь строится сопоставлением текста (`db.query.text`), а Database Observability входит в free tier (до 3 хостов БД; у нас один сервер).

Сейчас этого нет по двум причинам:

- `orders` отключает текст statement в otelpgx (`WithDisableSQLStatementInAttributes`), `delivery` его не пишет. Это перестраховка из правила `docs/observability.md` («захват текста statement отключается или санитизируется»), а не анализ риска: все запросы параметризованы, значений в тексте нет.
- Alloy собирает с PostgreSQL только метрики `prometheus.exporter.postgres`; `pg_stat_statements` не загружен.

## What Changes

- **Текст запроса в client spans.** `orders` (otelpgx) и `delivery` (`pkg/telemetry/db.rs`) пишут `db.query.text` — параметризованный текст с `$n` — и `db.namespace`. Bind parameters по-прежнему не записываются.
- **PostgreSQL kind.** `shared_preload_libraries=pg_stat_statements`, `compute_query_id=on`, `pg_stat_statements.track=all`, `track_activity_query_size=4096`. Расширение `pg_stat_statements` — в базе `postgres`, к которой подключается Alloy. Роль `alloy` получает `pg_read_all_data` (explain plans и схема) и не попадает в `pg_stat_statements`.
- **Alloy.** Один `database_observability.postgres` на сервер вместо трёх `prometheus.exporter.postgres` по владельцам: query details, query samples, schema details, explain plans. Метрики встроенного postgres_exporter (`database`, `stat_database`, `replication_slot`, `stat_statements`) и `database_observability_*` идут прежним OTLP-путём с `job="integrations/db-o11y"`; label `owner` берётся из `datname` и `slot_name`. Логи компонента — в Loki stack напрямую (`loki.write`): приложение ищет их по index label `job`, а OTLP-путь делает labels structured metadata. Alloy обновляется до v1.21.3.
- **Dashboards и alerts.** Platform / Overview и alert rules PostgreSQL читают `job="integrations/db-o11y"`; `pg_up` — по серверу, а не по владельцу.
- **Документация.** `docs/observability.md` (правило текста statement), ADR 9, README `orders`, `delivery`, `ops/grafana-cloud`, инвентарь метрик.

Не входит:

- sqlcommenter (`traceparent` в комментарии SQL): ломает кеш prepared statements pgx и sqlx, в `delivery` несовместим с `sqlx::query!`, а точное совпадение даёт только для запросов, пойманных опросом раз в 10 секунд (design.md);
- логи сервера PostgreSQL в компонент (`enable_error_logs_processing`, `log_line_prefix`);
- client spans операций `payments` (psycopg не инструментирован) и MongoDB `restaurants`.

## Capabilities

### New Capabilities

- `database-observability`: текст запроса в client spans PostgreSQL, сбор Database Observability в тестовом окружении, trace linkage.

### Modified Capabilities

Нет.

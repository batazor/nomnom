# Design

## Текст запроса в spans

Semantic conventions рекомендуют `db.query.text` для параметризованных запросов: значения передаются bind parameters, в тексте только `$n`. У нас других запросов нет:

- `orders` строит SQL через squirrel с placeholder `$n` и выполняет pgx; otelpgx пишет `db.query.text` = `data.SQL`, `db.namespace` = база соединения. Опцию `WithDisableSQLStatementInAttributes` убираем, `WithIncludeQueryParameters` не включаем.
- `delivery` выполняет только литералы `sqlx::query!` внутри `sql!`; `Statement::of_source` уже достаёт литерал из `stringify!`. Текст сохраняется в `Statement` один раз на call site (`OnceLock`), escape-последовательности обычного литерала раскрываются (`\"`, `\\`, `\n`, `\t`, перенос строки после `\`), raw-литерал берётся как есть. Без литерала (`of_sql` для произвольного текста) текстом считается сам statement. `db.namespace` — имя базы из URI (`PgConnectOptions::get_database`); URI без базы атрибут не даёт.

Константы в тексте (`status = 'pending'`) — часть кода, а не данных. Spans крупнее на длину запроса; это объём traces в квоте free tier (50 GB), не cardinality: текст не label метрик.

## Сопоставление по тексту, без sqlcommenter

Trace linkage сопоставляет span с запросом по `db.query.text`, `db.system.name` и `db.namespace`, учитывая разные placeholders, нормализацию и форматирование. Точное совпадение по trace/span ID требует sqlcommenter — `traceparent` в комментарии SQL. Не берём:

- текст каждого выполнения уникален, и кеш prepared statements по тексту (pgx `QueryExecModeCacheStatement`, sqlx) перестаёт работать: на каждом запросе parse и planning на сервере и лишний round trip, кеш вытесняется `DEALLOCATE`; ради этого кеша pooler `orders` держится в session mode;
- `sqlx::query!` проверяет литерал на этапе компиляции, комментарий во время выполнения требует уйти с макросов;
- Alloy видит выполнение, только если оно шло в момент опроса `pg_stat_activity` (раз в 10 секунд), то есть практически только медленные запросы.

Вернуться к нему можно точечно в `orders` (одна точка `exec`/`query`/`queryRow` в `store.go`), если понадобится переход sample → trace.

## Один компонент на сервер

Коллекторы `database_observability.postgres` работают на уровне сервера: query samples читают `pg_stat_activity` всех баз, query details — `pg_stat_statements` всех баз, schema details подключаются к каждой базе по тому же DSN. Компонент на каждую базу владельца утроил бы samples и детали, а v1.21 прямо запрещает два `database_instance` одного сервера. Поэтому один компонент:

- DSN — роль `alloy`, база `postgres` (в `pg_hba` добавляется строка `host postgres alloy`), Secret `alloy-postgres` из chart `ops/kind/bootstrap` с ключом `POSTGRES_DSN` вместо `POSTGRES_<OWNER>_DSN`;
- `exclude_databases = ["postgres"]`: служебная база не попадает ни в коллекторы, ни во встроенный экспортер;
- `pg_stat_statements` создаётся в `postgres`: view читает общую память сервера, в базах владельцев расширение не нужно, и их схемы не меняются.

Права `alloy`: `pg_monitor` (в нём `pg_read_all_stats`) и `pg_read_all_data`. Explain plan и схема требуют чтения таблиц; таблицы создают миграции владельцев позже bootstrap, поэтому предопределённая роль проще default privileges по схемам, а доступ тот же. `ALTER ROLE alloy SET pg_stat_statements.track = 'none'` убирает запросы самого Alloy из статистики. Это тестовое окружение (ADR 8): база — `emptyDir`, init-скрипт выполняется на каждом новом pod.

## Метрики и dashboards

Встроенный экспортер — тот же postgres_exporter, имена метрик прежние. Меняются labels:

- `job` = `integrations/db-o11y` — его задаёт компонент, по нему приложение находит экземпляр; dashboards и alerts переходят на него;
- `instance` = `postgres` (сервер);
- `owner` — из `datname` (`stat_database`, `database`, `stat_statements`) и из `slot_name` `<owner>_outbox` (`replication_slot`); у `pg_up` и `database_observability_connection_info` владельца нет.

Allowlist `prometheus.relabel` расширяется на `pg_stat_statements_*` (верхние 100 запросов, `exclude_users = ["alloy"]`) и `database_observability_*`. `pg_up` в Platform / Overview и в alert `postgres-down` — по `instance`, без фильтра `$owner`.

## Логи компонента

Компонент отдаёт Loki entries с labels `job`, `instance`, `server_id`, `engine`; приложение ищет их stream selector по `job`. OTLP-путь Alloy (`otelcol.receiver.loki` → OTLP gateway) превращает labels записи в structured metadata, а не в index labels. Поэтому логи компонента идут `loki.write` прямо в Loki stack:

- endpoint и user — новые `GRAFANA_CLOUD_LOGS_URL` и `GRAFANA_CLOUD_LOGS_USER` в `.env.grafana-cloud` (datasource `grafanacloud-logs` stack);
- token — тот же `GRAFANA_CLOUD_OTLP_TOKEN`: OTLP-логи уже требуют у него `logs:write`;
- init container `require-grafana-cloud` проверяет и новые переменные: без них Alloy не стартует.

Метрики остаются на OTLP-пути: Grafana Cloud восстанавливает `job` и `instance` из resource attributes, как сейчас у `infra/*`.

## Риски

- Объём логов: samples каждые 10 секунд, детали и explain plans раз в минуту, schema раз в минуту с повтором не чаще 30 минут на объект. Проверяется в Cost management после включения; при росте — `collect_interval`.
- Приложение может опираться на labels или метрики, которых нет в allowlist; проверяется открытием Database Observability на данных kind.

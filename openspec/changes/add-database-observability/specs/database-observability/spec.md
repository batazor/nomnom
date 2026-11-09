# Spec Delta

## ADDED Requirements

### Requirement: Client span PostgreSQL несёт текст запроса
**ID:** FR-769

Client span операции PostgreSQL в `orders` и `delivery` SHALL содержать `db.system.name` = `postgresql`, `db.namespace` — имя базы соединения — и `db.query.text` — параметризованный текст statement с placeholders `$n`. Bind parameters MUST NOT записываться ни в один attribute span.

#### Scenario: Запрос orders
**ID:** AC-1083

- **GIVEN** use case `orders` выполняется внутри trace
- **WHEN** repository выполняет `SELECT ... WHERE id = $1` со значением параметра
- **THEN** client span содержит `db.query.text` с `$1`, `db.namespace` = `orders`, `db.system.name` = `postgresql`
- **AND** значения параметра нет ни в одном attribute span

#### Scenario: Запрос delivery
**ID:** AC-1084

- **GIVEN** use case `delivery` выполняется внутри trace
- **WHEN** store выполняет литерал `sqlx::query!` через `sql!`
- **THEN** client span содержит `db.query.text`, равный тексту литерала с раскрытыми escape-последовательностями, и `db.namespace` = `delivery`

### Requirement: PostgreSQL тестового окружения отдаёт статистику запросов
**ID:** FR-770

Сервер PostgreSQL kind SHALL загружать `pg_stat_statements` (`shared_preload_libraries`), считать `compute_query_id`, учитывать вложенные statements (`pg_stat_statements.track = all`) и хранить в `pg_stat_activity` до 4096 байт текста запроса. Расширение `pg_stat_statements` MUST быть создано в базе `postgres`. Роль `alloy` SHALL иметь `pg_monitor` и `pg_read_all_data`, а её собственные запросы MUST NOT учитываться в `pg_stat_statements`.

#### Scenario: Статистика доступна Alloy
**ID:** AC-1085

- **GIVEN** окружение развёрнуто и сервисы выполнили запросы к своим базам
- **WHEN** роль `alloy` читает `pg_stat_statements` в базе `postgres`
- **THEN** видны запросы баз `orders`, `payments` и `delivery`, и нет запросов роли `alloy`

### Requirement: Alloy собирает Database Observability сервера
**ID:** FR-771

Профиль `grafana-cloud` SHALL запускать один `database_observability.postgres` на сервер PostgreSQL с коллекторами query details, query samples, schema details и explain plans, без базы `postgres`. Логи компонента MUST отправляться в Loki stack с index label `job` = `integrations/db-o11y`. Метрики встроенного экспортера SHALL иметь `job` = `integrations/db-o11y`, `instance` = `postgres` и `owner` для метрик базы владельца и её слота. Пустой endpoint или user Loki MUST останавливать запуск Alloy с сообщением, какие переменные не заданы.

#### Scenario: Samples запросов в Loki
**ID:** AC-1086

- **GIVEN** окружение развёрнуто с `GRAFANA_CLOUD=1` и сервисы выполняют запросы
- **WHEN** в Loki stack выполняется запрос `{job="integrations/db-o11y"}`
- **THEN** есть записи `op="query_association"` с `datname` баз владельцев и записи `op="create_statement"` их таблиц

#### Scenario: Не заданы учётные данные Loki
**ID:** AC-1087

- **GIVEN** в `.env.grafana-cloud` пуст `GRAFANA_CLOUD_LOGS_URL` или `GRAFANA_CLOUD_LOGS_USER`
- **WHEN** выполняется `make kind-deploy GRAFANA_CLOUD=1`
- **THEN** init container `require-grafana-cloud` завершается ошибкой с именами переменных, и Alloy не запускается

### Requirement: Platform / Overview читает метрики Database Observability
**ID:** FR-772

Row PostgreSQL Platform / Overview и alert rules PostgreSQL и слотов репликации SHALL выбирать метрики по `job="integrations/db-o11y"`. Доступность сервера (`pg_up`) MUST показываться и проверяться по `instance`, а метрики баз и слотов — по `owner`.

#### Scenario: Dashboard на данных kind
**ID:** AC-1088

- **GIVEN** окружение развёрнуто с `GRAFANA_CLOUD=1`
- **WHEN** открыт Platform / Overview, row PostgreSQL
- **THEN** панель Up показывает сервер `postgres`, а Connections, Transactions и Replication slot lag — ряды `orders`, `payments`, `delivery`

### Requirement: DB span связан с запросом в Database Observability
**ID:** FR-773

Database span `orders` или `delivery` в Grafana Cloud Traces SHALL открывать запрос в Database Observability по совпадению текста запроса.

#### Scenario: Переход от span к запросу
**ID:** AC-1089

- **GIVEN** trace use case `orders` с client span PostgreSQL в Grafana Cloud Traces
- **WHEN** в Explore открыт этот span
- **THEN** у текста запроса есть ссылка Database Observability, и она открывает этот запрос сервера `postgres`

# Runbooks

Что делать при сработавшем alert'е. Каждое правило ([ADR 29](adr/0029-grafana-dashboards-as-code.md), [ADR 40](adr/0040-service-slo-and-burn-rate-alerts.md)) ссылается на раздел этого документа через annotation `runbook_url`, название раздела и есть якорь. Ссылка на dashboard — в annotation `dashboard_url`.

Команды даны для kind (контекст `kind-nomnom-test`); в другом окружении меняются контекст и адреса. Метрики и пороги — в README dashboards `boundaries/<service>/ops/grafana/` и [`ops/grafana`](../ops/grafana/README.md). Как связать метрику, trace и лог — [Observability](observability.md).

Severity: `critical` — пользовательский путь сломан, реагировать сейчас; `warning` — деградация или риск, разбирается в рабочее время.

## service-availability

Сервис отвечает техническими кодами (`UNAVAILABLE`, `INTERNAL`, `DEADLINE_EXCEEDED`…) или быстро тратит бюджет SLO Availability. Бизнес-отказы сюда не входят.

1. Открыть dashboard сервиса, row Service health: какой метод и какой код.
2. Посмотреть логи и pods:

   ```bash
   kubectl -n nomnom-orders get pods
   kubectl -n nomnom-orders logs deploy/orders --since=15m
   ```

3. Проверить зависимости сервиса по [схеме контекста](architecture/README.md): хранилище ([postgres](#postgres)), [spicedb](#spicedb), internal gateway, Temporal. State-changing операции fail closed: отказ SpiceDB выглядит как ошибки сервиса.
4. Если alert совпал с деплоем — откатить release: `helm -n <namespace> rollback <release>`.

Окно `1h и 5m` (critical) значит, что 2% месячного бюджета уже потрачено и расход продолжается; `6h и 30m` (warning) — медленная утечка.

## service-latency

p95 вызовов выше порога или бюджет SLO Latency тратится быстро.

1. Dashboard сервиса, row Service health: какой метод, растёт ли CPU и memory.
2. Найти медленный trace этого метода в Tempo и посмотреть, какой span занимает время: БД, SpiceDB, соседний контекст.
3. Если это ожидание соединения — [connection-pool](#connection-pool); если CPU упирается в limit — проверить profile в Pyroscope.

## neighbour-errors

`orders` получает технические ошибки от соседних контекстов через internal gateway. Workflow Fulfillment повторяет Activities сам, заказы при этом ждут.

1. Row dashboard `orders` показывает соседа и метод; дальше — [service-availability](#service-availability) этого соседа.
2. Если сосед здоров, проверить gateway и его маршруты:

   ```bash
   kubectl -n envoy-gateway-system get pods
   kubectl get grpcroute -A
   ```

## usecase-errors

Use cases `orders` завершаются технической ошибкой (`outcome=error`), бизнес-отказы сюда не входят.

1. Dashboard `orders`: какой use case. Лог с тем же именем и его trace покажут причину.
2. Самые частые причины — база ([postgres](#postgres)) и конфликт транзакций ([ADR 20](adr/0020-unit-of-work-for-local-transactions.md)).

## dead-letters

Сообщение ушло в `<stream>.dlq`, само оно не повторится. Alert `unresolved dead letters` значит, что записи не разобраны больше суток; retention DLQ — 30 дней.

```bash
kubectl -n nomnom-orders exec deploy/orders-consumer -- /orders-admin dlq list --stream orders.v1.order-events
```

Дальше — redrive или discard по процедуре [Разбор DLQ](dlq.md).

## cache-bypass

Больше половины чтений идут мимо Valkey. Данные читаются из хранилища владельца, поэтому пользователи видят в первую очередь рост задержки, а база получает лишнюю нагрузку.

```bash
kubectl -n nomnom-data get pods
kubectl -n nomnom-data logs deploy/valkey-restaurants --since=15m
```

Если Valkey работает, сравнить его задержку с таймаутом `VALKEY__COMMAND_TIMEOUT` и проверить NetworkPolicy. Кеш восстанавливается сам ([ADR 43](adr/0043-application-caching-policy.md)).

## connection-pool

Пул соединений процесса насыщен, операции ждут или не получают соединение. У MongoDB отдельный сигнал: драйвер очистил пул из-за failover или сетевого сбоя.

1. Dashboard сервиса, row пула: насыщение от нагрузки или от долгих транзакций. Долгие транзакции видны в traces.
2. PostgreSQL ходит через PgBouncer:

   ```bash
   kubectl -n nomnom-data get deploy pgbouncer postgres
   kubectl -n nomnom-data logs deploy/pgbouncer --since=15m
   ```

3. Размер пула менять только после того, как исключены долгие транзакции ([ADR 39](adr/0039-data-access-without-orm.md)).

## delivery-failures

Больше 20% отказов Delivery идут с причиной вне каталога. Это сигнал о новой причине: её нужно внести в каталог или исправить источник. Причины и их значения — [README `delivery`](../boundaries/delivery/README.md).

## spicedb

SpiceDB отвечает ошибками или медленно. Он глобальный: отказ задевает все Market, а клиенты fail closed и отказывают в доступе.

```bash
kubectl -n nomnom-authz get pods
kubectl -n nomnom-authz logs deploy/spicedb --since=15m
```

Проверить хранилище SpiceDB и недавние изменения схемы `authorization/schema.zed` ([ADR 18](adr/0018-spicedb-for-authorization.md)).

## postgres

База владельца недоступна или в ней deadlocks. Один PostgreSQL держит базы `orders`, `delivery` и `payments`.

```bash
kubectl -n nomnom-data get pods
kubectl -n nomnom-data logs deploy/postgres --since=15m
kubectl -n nomnom-data exec deploy/postgres -- psql -U postgres -c 'select datname, numbackends, deadlocks from pg_stat_database'
```

Deadlocks: найти запросы в логе PostgreSQL и use case по trace; порядок блокировок правится в коде контекста.

## cdc

Debezium не доставляет Outbox или отстаёт ([ADR 37](adr/0037-outbox-delivery-via-debezium-cdc.md)). Записи при этом не теряются, но события и подписки задерживаются, а слот PostgreSQL копит WAL и заполняет диск.

```bash
kubectl -n nomnom-infra get kafkaconnector
kubectl -n nomnom-infra exec kafka-connect-connect-0 -- curl -s localhost:8083/connectors/orders-outbox/status
kubectl -n nomnom-data exec deploy/postgres -- psql -U postgres -c 'select slot_name, active, pg_size_pretty(pg_current_wal_lsn() - restart_lsn) from pg_replication_slots'
```

Коннекторы: `orders-outbox`, `restaurants-outbox`, `delivery-outbox`, `payments-outbox`. Запись, нарушающая контракт, останавливает коннектор (head-of-line): причина — в `trace` статуса. Исправить её и перезапустить задачу:

```bash
kubectl -n nomnom-infra exec kafka-connect-connect-0 -- curl -s -X POST 'localhost:8083/connectors/orders-outbox/restart?includeTasks=true'
```

Слот, который долго неактивен, держит WAL: сначала чинить коннектор, а не удалять слот. Удаление слота теряет позицию чтения.

## pods

Контейнер в CrashLoopBackOff, часто перезапускается или не проходит readiness.

```bash
kubectl get pods -A --field-selector=status.phase!=Running
kubectl -n <namespace> describe pod <pod>
kubectl -n <namespace> logs <pod> --previous
```

Обязательный конфиг пуст или невалиден — процесс падает на старте, причина в первых строках лога. OOMKilled в `describe` — смотреть limit в values release. Контракт health — [Cloud-native runtime](cloud-native-runtime.md).

## certificates

Certificate не Ready или скоро истекает: cert-manager его не продлил.

```bash
kubectl get certificate -A
kubectl -n <namespace> describe certificate <name>
kubectl -n cert-manager logs deploy/cert-manager --since=1h
```

Причина — в событиях Certificate и его CertificateRequest.

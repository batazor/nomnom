<!-- Сгенерировано ops/grafana (make grafana-generate). Не редактировать вручную: источник — ops/grafana/inventory/inventory.go. -->

# Инвентарь метрик

Инвентарь фиксирует метрики, на которых строятся overview-dashboards сервисов
([docs/observability.md](../observability.md#dashboards)). Панель строится только на метриках
со статусом «есть»; для остальных генератор рисует TODO-панель со ссылкой на раздел этого документа.

| Статус | Значение |
| --- | --- |
| есть | Метрика экспортируется сервисом по OTLP, а имя и labels подтверждены приёмом через Alloy в Grafana Cloud. |
| ожидается | Метрика принята архитектурой (semconv, ADR, решение по Watermill), но не экспортируется или не доставляется. |

Имена приведены в виде Prometheus после OTLP-трансляции (точки → подчёркивания, unit suffix, `_total` для
монотонных counters); Grafana Cloud добавляет suffixes при OTLP ingestion. Метрики Prometheus bridge (Watermill) не меняются. Для histogram указано базовое имя; запросы используют `_bucket`/`_count`/`_sum`.

## Состояние на дату генерации

Метрик в инвентаре: 143; пар «метрика × сервис» со статусом «есть»: 141.

Подтверждение статуса «есть»: 2026-10-25: kind → OTLP → Grafana Alloy → Grafana Cloud (`ops/grafana-cloud`): подтверждены метрики `orders`, `orders-worker`, `orders-consumer`, а также rpc server/client, use case, process и db client метрики `restaurants`, `delivery`, `payments` (имена и labels — запросом series).

Метрики подключены в процессах `orders`: `orders` (gRPC server), `orders-worker` (Temporal, gRPC client
к gateway) и `orders-consumer` (Watermill, OTel middleware), во всех трёх — Go runtime и process CPU через
`orders/pkg/telemetry`. Остальные контексты OpenTelemetry SDK пока не подключают, поэтому их панели — TODO.

<a id="resource"></a>

## Resource и идентичность

OTel resource в Prometheus: `job="nomnom/<service.name>"`, `instance=<service.instance.id>`, остальные resource attributes — в `target_info`. Promotion resource attributes не используется (docs/observability.md), поэтому environment и version фильтруются join с `target_info`.

| Метрика | Источник | Тип | Labels | Сервис → статус | Примечание |
| --- | --- | --- | --- | --- | --- |
| `target_info` | OTel resource → OTLP ingestion | gauge | `job`, `instance`, `deployment_environment_name`, `service_version` | `orders`: есть<br>`orders-worker`: есть<br>`orders-consumer`: есть<br>`restaurants`: есть<br>`delivery`: есть<br>`payments`: есть<br>`bff`: ожидается | Нужна для variables `environment`/`version`/`service_instance`, фильтра environment и deployment annotations. |

<a id="rpc"></a>

## gRPC (server и client через internal gateway)

Актуальные semconv RPC во всех контекстах: `rpc.server.call.duration`/`rpc.client.call.duration` в секундах, статус — имя кода в `rpc_response_status_code`, сервис и метод — в `rpc_method`. `orders` — `otelgrpc` (stats handler); `restaurants` — Connect server interceptor и grpc-js client interceptor; `payments` — собственные grpcio interceptors; `delivery` — tower layer сервера и клиент SpiceDB. Явные границы buckets в секундах.

| Метрика | Источник | Тип | Labels | Сервис → статус | Примечание |
| --- | --- | --- | --- | --- | --- |
| `rpc_server_call_duration_seconds` | `rpc.server.call.duration` (otelgrpc v0.72) | histogram | `rpc_system_name`, `rpc_method`, `rpc_response_status_code` | `orders`: есть<br>`restaurants`: есть<br>`delivery`: есть<br>`payments`: есть | `rpc_method` — `Service/Method`; `rpc_response_status_code` — имя кода (`OK`, `NOT_FOUND`). Инструментации других runtime закрепляются на тех же semconv-именах. |
| `rpc_client_call_duration_seconds` | `rpc.client.call.duration` (otelgrpc v0.72) | histogram | `rpc_system_name`, `rpc_method`, `rpc_response_status_code`, `error_type`, `server_address`, `server_port` | `orders`: есть<br>`orders-worker`: есть<br>`restaurants`: есть<br>`delivery`: есть<br>`payments`: есть<br>`bff`: ожидается | Вызовы через internal gateway (ADR 15); `server_address`/`server_port` — адрес gateway. SpiceDB — в row Authorization. |

<a id="graphql"></a>

## GraphQL / HTTP (BFF, Cosmo Router)

Встроенная telemetry Cosmo Router: OTLP к Alloy в профиле grafana-cloud (`BFF__METRICS_OTLP_ENABLED`). Alloy оставляет три инструмента и labels `wg_operation_type`, `wg_subgraph_name`, `http_status_code` — operation name, клиент и версии router отбрасываются ради квоты series.

| Метрика | Источник | Тип | Labels | Сервис → статус | Примечание |
| --- | --- | --- | --- | --- | --- |
| `router_http_requests_total` | Cosmo `router.http.requests` | counter | `wg_operation_type`, `wg_subgraph_name`, `http_status_code` | `bff`: ожидается | — |
| `router_http_requests_error_total` | Cosmo `router.http.requests.error` | counter | `wg_operation_type`, `wg_subgraph_name`, `http_status_code` | `bff`: ожидается | GraphQL errors в ответе 200. |
| `router_http_request_duration_milliseconds` | Cosmo `router.http.request.duration_milliseconds` | histogram | `wg_operation_type`, `wg_subgraph_name`, `http_status_code` | `bff`: ожидается | Без `wg_subgraph_name` — клиентский запрос, с ним — вызов subgraph (BFF plugin → internal gateway). |

<a id="runtime"></a>

## Runtime

Go: `go.opentelemetry.io/contrib/instrumentation/runtime` (semconv runtime Go). Node.js: `@opentelemetry/instrumentation-runtime-node` и `process.*`/`v8js.memory.heap.limit` своими callbacks. Python и Rust: `process.cpu.time`, `process.memory.usage`, `process.thread.count` своими callbacks (Python — ещё `cpython.gc.collections`; Rust читает procfs), потому что готовые instrumentations пишут устаревшие labels. Метрики tokio runtime не имеют semconv и не инвентаризированы.

| Метрика | Источник | Тип | Labels | Сервис → статус | Примечание |
| --- | --- | --- | --- | --- | --- |
| `process_cpu_time_seconds_total` | `process.cpu.time` | counter | `cpu_mode` | `orders`: есть<br>`orders-worker`: есть<br>`orders-consumer`: есть<br>`restaurants`: есть<br>`delivery`: есть<br>`payments`: есть | Go: `go.opentelemetry.io/contrib/instrumentation/host` (метрики `system.*` отбрасываются View). |
| `process_memory_usage_bytes` | `process.memory.usage` | gauge | — | `restaurants`: есть<br>`delivery`: есть<br>`payments`: есть | Для Go не экспортируется; память Go-ролей — `go_memory_used_bytes`. |
| `process_thread_count` | `process.thread.count` | gauge | — | `delivery`: ожидается<br>`payments`: ожидается | Python: worker model gRPC thread pool. |
| `go_memory_used_bytes` | `go.memory.used` | gauge | `go_memory_type` | `orders`: есть<br>`orders-worker`: есть<br>`orders-consumer`: есть | — |
| `go_memory_allocated_bytes_total` | `go.memory.allocated` | counter | — | `orders`: есть<br>`orders-worker`: есть<br>`orders-consumer`: есть | — |
| `go_memory_gc_goal_bytes` | `go.memory.gc.goal` | gauge | — | `orders`: есть<br>`orders-worker`: есть<br>`orders-consumer`: есть | — |
| `go_memory_limit_bytes` | `go.memory.limit` | gauge | — | `orders`: ожидается<br>`orders-worker`: ожидается<br>`orders-consumer`: ожидается | GOMEMLIMIT; automemlimit выставляет 90% memory limit контейнера. |
| `go_goroutine_count` | `go.goroutine.count` | gauge | — | `orders`: есть<br>`orders-worker`: есть<br>`orders-consumer`: есть | — |
| `go_processor_limit` | `go.processor.limit` | gauge | — | `orders`: есть<br>`orders-worker`: есть<br>`orders-consumer`: есть | — |
| `v8js_memory_heap_used_bytes` | `v8js.memory.heap.used` | gauge | `v8js_heap_space_name` | `restaurants`: ожидается | — |
| `v8js_memory_heap_limit_bytes` | `v8js.memory.heap.limit` | gauge | `v8js_heap_space_name` | `restaurants`: ожидается | — |
| `nodejs_eventloop_delay_p99_seconds` | `nodejs.eventloop.delay.p99` | gauge | — | `restaurants`: ожидается | — |
| `v8js_gc_duration_seconds` | `v8js.gc.duration` | histogram | `v8js_gc_type` | `restaurants`: ожидается | — |
| `cpython_gc_collections_total` | `cpython.gc.collections` | counter | `cpython_gc_generation` | `payments`: ожидается | — |

<a id="db"></a>

## Database client (PostgreSQL, MongoDB)

Semconv `db.client.*`: `pgx` + otelpgx в `orders`, MongoDB Node.js driver в `restaurants`: операции — instrumentation драйвера, пул — CMAP events (`db.client.connection.*` и `nomnom.db.client.connection.pool.clears`). Пул PostgreSQL `orders` — tracer и `Pool.Stat` pgx, `payments` — `psycopg_pool` (`get_stats` и замер выдачи соединения); операции `payments` (psycopg instrumentation) не подключены. Пул `delivery` — sqlx (состояние пула при сборе и замер выдачи соединения), операции `delivery` — `sql!` хранилища (`db.client.operation.duration` и client span на statement, `pkg/telemetry/db.rs`). Серверные метрики PostgreSQL — Platform / Overview (раздел [PostgreSQL](#postgresql)), MongoDB — пока нет; в overview сервисов не дублируются.

| Метрика | Источник | Тип | Labels | Сервис → статус | Примечание |
| --- | --- | --- | --- | --- | --- |
| `db_client_operation_duration_seconds` | `db.client.operation.duration` | histogram | `db_system_name`, `db_operation_name`, `db_collection_name`, `error_type` | `orders`: есть<br>`orders-worker`: есть<br>`orders-consumer`: есть<br>`restaurants`: есть<br>`delivery`: есть<br>`payments`: ожидается | — |
| `db_client_connection_count` | `db.client.connection.count` | gauge | `db_client_connection_pool_name`, `db_client_connection_state` | `orders`: есть<br>`orders-worker`: есть<br>`orders-consumer`: есть<br>`restaurants`: есть<br>`delivery`: есть<br>`payments`: есть | `orders`: tracer и `Pool.Stat` pgx (`slices/order/infrastructure/repository/pool_metrics.go`), `payments`: `psycopg_pool` (`infrastructure/repository/pool_metrics.py`), `delivery`: sqlx-пул (`infrastructure/repository/pool.rs`; `create_time` — от начала acquire до `after_connect`, ожидание свободного слота включено; пишут `delivery` и `delivery-relay`); `db_client_connection_pool_name` = имя контекста, роль различает `job`. |
| `db_client_connection_max` | `db.client.connection.max` | gauge | `db_client_connection_pool_name` | `orders`: ожидается<br>`orders-worker`: ожидается<br>`orders-consumer`: ожидается<br>`restaurants`: ожидается<br>`delivery`: ожидается<br>`payments`: ожидается | `orders`: tracer и `Pool.Stat` pgx (`slices/order/infrastructure/repository/pool_metrics.go`), `payments`: `psycopg_pool` (`infrastructure/repository/pool_metrics.py`), `delivery`: sqlx-пул (`infrastructure/repository/pool.rs`; `create_time` — от начала acquire до `after_connect`, ожидание свободного слота включено; пишут `delivery` и `delivery-relay`); `db_client_connection_pool_name` = имя контекста, роль различает `job`. |
| `db_client_connection_wait_time_seconds` | `db.client.connection.wait_time` | histogram | `db_client_connection_pool_name` | `orders`: ожидается<br>`orders-worker`: ожидается<br>`orders-consumer`: ожидается<br>`restaurants`: ожидается<br>`delivery`: ожидается<br>`payments`: ожидается | `orders`: tracer и `Pool.Stat` pgx (`slices/order/infrastructure/repository/pool_metrics.go`), `payments`: `psycopg_pool` (`infrastructure/repository/pool_metrics.py`), `delivery`: sqlx-пул (`infrastructure/repository/pool.rs`; `create_time` — от начала acquire до `after_connect`, ожидание свободного слота включено; пишут `delivery` и `delivery-relay`); `db_client_connection_pool_name` = имя контекста, роль различает `job`. |
| `db_client_connection_idle_min` | `db.client.connection.idle.min` | gauge | `db_client_connection_pool_name` | `orders`: ожидается<br>`orders-worker`: ожидается<br>`orders-consumer`: ожидается<br>`restaurants`: ожидается<br>`delivery`: ожидается<br>`payments`: ожидается | `restaurants`: CMAP events драйвера (`infrastructure/repository/pool-metrics.ts`), пул на каждый сервер replica set, `db_client_connection_pool_name` = `<host:port>/<database>`; те же метрики пишут `restaurants-relay` и `restaurants-consumer`. `orders`: tracer и `Pool.Stat` pgx (`slices/order/infrastructure/repository/pool_metrics.go`), `payments`: `psycopg_pool` (`infrastructure/repository/pool_metrics.py`), `delivery`: sqlx-пул (`infrastructure/repository/pool.rs`; `create_time` — от начала acquire до `after_connect`, ожидание свободного слота включено; пишут `delivery` и `delivery-relay`); `db_client_connection_pool_name` = имя контекста, роль различает `job`. |
| `db_client_connection_pending_requests` | `db.client.connection.pending_requests` | gauge | `db_client_connection_pool_name` | `orders`: ожидается<br>`orders-worker`: ожидается<br>`orders-consumer`: ожидается<br>`restaurants`: ожидается<br>`delivery`: ожидается<br>`payments`: ожидается | Checkouts, ждущие соединения пула. `restaurants`: CMAP events драйвера (`infrastructure/repository/pool-metrics.ts`), пул на каждый сервер replica set, `db_client_connection_pool_name` = `<host:port>/<database>`; те же метрики пишут `restaurants-relay` и `restaurants-consumer`. `orders`: tracer и `Pool.Stat` pgx (`slices/order/infrastructure/repository/pool_metrics.go`), `payments`: `psycopg_pool` (`infrastructure/repository/pool_metrics.py`), `delivery`: sqlx-пул (`infrastructure/repository/pool.rs`; `create_time` — от начала acquire до `after_connect`, ожидание свободного слота включено; пишут `delivery` и `delivery-relay`); `db_client_connection_pool_name` = имя контекста, роль различает `job`. |
| `db_client_connection_timeouts_total` | `db.client.connection.timeouts` | counter | `db_client_connection_pool_name` | `orders`: ожидается<br>`orders-worker`: ожидается<br>`orders-consumer`: ожидается<br>`restaurants`: ожидается<br>`delivery`: ожидается<br>`payments`: ожидается | Checkout не получил соединение за wait timeout (MongoDB `waitQueueTimeoutMS`). `restaurants`: CMAP events драйвера (`infrastructure/repository/pool-metrics.ts`), пул на каждый сервер replica set, `db_client_connection_pool_name` = `<host:port>/<database>`; те же метрики пишут `restaurants-relay` и `restaurants-consumer`. `orders`: tracer и `Pool.Stat` pgx (`slices/order/infrastructure/repository/pool_metrics.go`), `payments`: `psycopg_pool` (`infrastructure/repository/pool_metrics.py`), `delivery`: sqlx-пул (`infrastructure/repository/pool.rs`; `create_time` — от начала acquire до `after_connect`, ожидание свободного слота включено; пишут `delivery` и `delivery-relay`); `db_client_connection_pool_name` = имя контекста, роль различает `job`. |
| `db_client_connection_create_time_seconds` | `db.client.connection.create_time` | histogram | `db_client_connection_pool_name` | `orders`: ожидается<br>`orders-worker`: ожидается<br>`orders-consumer`: ожидается<br>`restaurants`: ожидается<br>`delivery`: ожидается<br>`payments`: ожидается | Установка соединения с handshake и auth. `restaurants`: CMAP events драйвера (`infrastructure/repository/pool-metrics.ts`), пул на каждый сервер replica set, `db_client_connection_pool_name` = `<host:port>/<database>`; те же метрики пишут `restaurants-relay` и `restaurants-consumer`. `orders`: tracer и `Pool.Stat` pgx (`slices/order/infrastructure/repository/pool_metrics.go`), `payments`: `psycopg_pool` (`infrastructure/repository/pool_metrics.py`), `delivery`: sqlx-пул (`infrastructure/repository/pool.rs`; `create_time` — от начала acquire до `after_connect`, ожидание свободного слота включено; пишут `delivery` и `delivery-relay`); `db_client_connection_pool_name` = имя контекста, роль различает `job`. |
| `db_client_connection_use_time_seconds` | `db.client.connection.use_time` | histogram | `db_client_connection_pool_name` | `orders`: ожидается<br>`orders-worker`: ожидается<br>`orders-consumer`: ожидается<br>`restaurants`: ожидается<br>`delivery`: ожидается<br>`payments`: ожидается | От checkout до возврата соединения в пул. `restaurants`: CMAP events драйвера (`infrastructure/repository/pool-metrics.ts`), пул на каждый сервер replica set, `db_client_connection_pool_name` = `<host:port>/<database>`; те же метрики пишут `restaurants-relay` и `restaurants-consumer`. `orders`: tracer и `Pool.Stat` pgx (`slices/order/infrastructure/repository/pool_metrics.go`), `payments`: `psycopg_pool` (`infrastructure/repository/pool_metrics.py`), `delivery`: sqlx-пул (`infrastructure/repository/pool.rs`; `create_time` — от начала acquire до `after_connect`, ожидание свободного слота включено; пишут `delivery` и `delivery-relay`); `db_client_connection_pool_name` = имя контекста, роль различает `job`. |
| `nomnom_db_client_connection_pool_clears_total` | MongoDB CMAP `connectionPoolCleared` | counter | `db_client_connection_pool_name` | `restaurants`: ожидается | В semconv нет: драйвер пометил сервер недоступным и закрыл соединения его пула (failover, сетевой сбой). `restaurants`: CMAP events драйвера (`infrastructure/repository/pool-metrics.ts`), пул на каждый сервер replica set, `db_client_connection_pool_name` = `<host:port>/<database>`; те же метрики пишут `restaurants-relay` и `restaurants-consumer`. |

<a id="cache"></a>

## Read cache (Valkey)

ADR 43: cache-aside GetRestaurant, GetMenu и ListRestaurants `restaurants` (`infrastructure/valkey`); `restaurants-consumer` обновляет ключи по событиям открытия, закрытия, уровня ожидания и изменений меню, остальное — TTL. Ключи и restaurant id не labels. `bypass` — Valkey недоступен, чтение из MongoDB.

| Метрика | Источник | Тип | Labels | Сервис → статус | Примечание |
| --- | --- | --- | --- | --- | --- |
| `nomnom_cache_lookups_total` | `nomnom.cache.lookups` | counter | `nomnom_cache_name`, `nomnom_cache_result` | `restaurants`: ожидается | hit ratio = hit / (hit + miss); доля bypass — время без Valkey. |
| `nomnom_cache_errors_total` | `nomnom.cache.errors` | counter | `nomnom_cache_name`, `nomnom_cache_operation` | `restaurants`: ожидается<br>`restaurants-consumer`: ожидается | `get`/`set` — чтение API, `refresh` — обновление по событию в `restaurants-consumer`. |
| `nomnom_cache_refreshes_total` | `nomnom.cache.refreshes` | counter | `nomnom_cache_name` | `restaurants-consumer`: ожидается | Пишет `restaurants-consumer` по RestaurantOpened/Closed, MenuItemAdded, MenuItemUpdated и MenuItemAvailabilityChanged. |

<a id="watermill"></a>

## Kafka: встроенные метрики Watermill

`components/metrics.PrometheusMetricsBuilder`, namespace `watermill`, subsystem — deployable role (`consumer`). Экспорт через OTel Prometheus bridge (`orders/pkg/telemetry`) в общий OTLP pipeline; имена Prometheus сохраняются без изменений. Референс панелей — Grafana dashboard 9777, отдельный dashboard не создаётся.

| Метрика | Источник | Тип | Labels | Сервис → статус | Примечание |
| --- | --- | --- | --- | --- | --- |
| `watermill_consumer_subscriber_messages_received_total` | `PrometheusMetricsBuilder` subsystem `consumer` | counter | `acked`, `handler_name`, `subscriber_name` | `orders-consumer`: есть | `acked`: acked | nacked. Через OTel Prometheus bridge (`pkg/telemetry`). |
| `watermill_consumer_handler_execution_time_seconds` | `PrometheusMetricsBuilder` subsystem `consumer` | histogram | `handler_name`, `success` | `orders-consumer`: есть | Снаружи DLQ: `success="false"` — только исчерпанный transient (nack); permanent уходит в DLQ и считается успехом. |
| `watermill_consumer_publish_time_seconds` | `PrometheusMetricsBuilder` subsystem `consumer` | histogram | `success`, `handler_name`, `publisher_name` | `orders-consumer`: есть | Publisher consumer-роли — dead letters в `<stream>.dlq`. |

<a id="messaging-otel"></a>

## Kafka: semconv messaging (не-Go контексты)

Kafka-клиенты: kafkajs (`restaurants`), confluent-kafka (`payments`), rdkafka (`delivery`). Trace context: Outbox хранит `traceparent` span, в котором событие записано; Debezium отдаёт его header `traceparent` без изменений (ADR 37); consumer — CONSUMER span `process <topic>` дочерним. Метрики semconv messaging не инвентаризированы.

| Метрика | Источник | Тип | Labels | Сервис → статус | Примечание |
| --- | --- | --- | --- | --- | --- |
| `messaging_client_consumed_messages_total` | `messaging.client.consumed.messages` | counter | `messaging_system`, `messaging_destination_name`, `messaging_consumer_group_name`, `error_type` | `restaurants`: ожидается | — |
| `messaging_client_sent_messages_total` | `messaging.client.sent.messages` | counter | `messaging_system`, `messaging_destination_name`, `error_type` | `restaurants`: ожидается | — |
| `messaging_client_operation_duration_seconds` | `messaging.client.operation.duration` | histogram | `messaging_system`, `messaging_operation_name`, `messaging_destination_name`, `error_type` | `restaurants`: ожидается | — |

<a id="messaging"></a>

## Kafka: собственный OTel middleware и consumer lag

Middleware в composition root поверх Watermill (и эквивалент в других runtime): то, чего нет во встроенных метриках. Порядок: OTel/metrics → Retry (transient) → PoisonQueue (permanent) → handler. Attributes только `nomnom.messaging.handler`, `nomnom.event.type`, `messaging.destination.name`, `nomnom.error.class`; без `event_id`/`aggregate_id`. Consumer lag — Alloy `prometheus.exporter.kafka`, не Watermill.

| Метрика | Источник | Тип | Labels | Сервис → статус | Примечание |
| --- | --- | --- | --- | --- | --- |
| `nomnom_messaging_process_duration_seconds` | OTel middleware `nomnom.messaging.process.duration` | histogram | `nomnom_messaging_handler`, `nomnom_event_type`, `messaging_destination_name`, `nomnom_error_class` | `orders-consumer`: есть<br>`restaurants`: ожидается | `error_class` только при неуспехе: permanent | deserialization | business | transient; успешный retry — без класса. `handler_name` — handler group. |
| `nomnom_messaging_retry_attempts_total` | OTel middleware `nomnom.messaging.retry.attempts` | counter | `nomnom_messaging_handler`, `nomnom_event_type`, `messaging_destination_name` | `orders-consumer`: есть<br>`restaurants`: ожидается | Ретраится только класс transient, поэтому `error_class` не пишется. |
| `nomnom_messaging_poison_messages_total` | OTel middleware вокруг PoisonQueue | counter | `nomnom_messaging_handler`, `nomnom_event_type`, `messaging_destination_name`, `nomnom_error_class` | `orders-consumer`: есть<br>`restaurants`: ожидается | Сообщения, отправленные в `<stream>.dlq`; `error_class`: permanent | deserialization. |
| `nomnom_messaging_message_age_seconds` | OTel middleware | histogram | `nomnom_messaging_handler`, `nomnom_event_type`, `messaging_destination_name` | `orders-consumer`: есть<br>`restaurants`: ожидается | `now − occurred_at` в момент обработки. |
| `nomnom_inbox_duplicates_total` | OTel middleware / Inbox adapter | counter | `nomnom_messaging_handler`, `nomnom_event_type`, `messaging_destination_name` | `orders-consumer`: есть<br>`restaurants`: ожидается | Outcome duplicate Inbox (`(consumer, event_id)` уже обработан). |
| `kafka_consumergroup_lag` | Alloy `prometheus.exporter.kafka` | gauge | `consumergroup`, `topic`, `partition` | `orders-consumer`: ожидается<br>`restaurants`: ожидается | Серверная метрика с job exporter'а; фильтр по `consumergroup` сервиса, без target_info join. |
| `kafka_consumergroup_current_offset` | Alloy `prometheus.exporter.kafka` | gauge | `consumergroup`, `topic`, `partition` | `orders-consumer`: ожидается<br>`restaurants`: ожидается | Группы `<group>.dlq` на `<stream>.dlq`: первая неразобранная запись DLQ группы (docs/dlq.md); −1 — у partition нет commit. |
| `kafka_topic_partition_current_offset` | Alloy `prometheus.exporter.kafka` | gauge | `topic`, `partition` | `orders-consumer`: ожидается<br>`restaurants`: ожидается | Только `<stream>.dlq`: конец partition DLQ. |
| `kafka_topic_partition_oldest_offset` | Alloy `prometheus.exporter.kafka` | gauge | `topic`, `partition` | `orders-consumer`: ожидается<br>`restaurants`: ожидается | Только `<stream>.dlq`: начало partition после retention; с него разбирает группа без commit. |

<a id="cdc"></a>

## Outbox CDC (Kafka Connect, Debezium)

Outbox публикует Debezium в Kafka Connect (ADR 37). Prometheus JMX exporter образа `ops/kind/kafka-connect` (правила `metrics.yaml`), Alloy scrape `:9404`, `job="infra/kafka-connect"`. Состояние коннектора и задачи — gauge 1 с label `status` (running, paused, failed, unassigned, restarting); `name` метрик Debezium — `topic.prefix` коннектора (`<context>-outbox`). Отставание слота репликации — раздел [PostgreSQL](#postgresql). Platform / Overview и row Kafka сервисов.

| Метрика | Источник | Тип | Labels | Сервис → статус | Примечание |
| --- | --- | --- | --- | --- | --- |
| `kafka_connect_connector_status` | Kafka Connect `connector-metrics` (JMX exporter) | gauge | `connector`, `status` | `kafka-connect`: есть | Имена и labels сняты scrape экспортера на стенде 2026-10-26 (Debezium 3.7.0.Final, JMX exporter 1.7.0, Alloy 1.21.2). |
| `kafka_connect_connector_task_status` | Kafka Connect `connector-task-metrics` (JMX exporter) | gauge | `connector`, `task`, `status` | `kafka-connect`: есть | Задача `failed` — доставка Outbox контекста остановлена. |
| `debezium_metrics_TotalNumberOfEventsSeen` | Debezium streaming metrics (JMX exporter) | counter | `name`, `plugin` | `kafka-connect`: есть | Счётчик с начала задачи; без суффикса `_total`. |
| `debezium_metrics_MilliSecondsBehindSource` | Debezium streaming metrics (JMX exporter) | gauge | `name`, `plugin` | `kafka-connect`: есть | От commit в базе до обработки последнего события, мс; −1 до первого события. |
| `debezium_metrics_MilliSecondsSinceLastEvent` | Debezium streaming metrics (JMX exporter) | gauge | `name`, `plugin` | `kafka-connect`: есть | Тишина — норма при отсутствии записей Outbox, сама по себе не сбой. |
| `debezium_metrics_QueueRemainingCapacity` | Debezium streaming metrics (JMX exporter) | gauge | `name`, `plugin` | `kafka-connect`: есть | — |
| `debezium_metrics_QueueTotalCapacity` | Debezium streaming metrics (JMX exporter) | gauge | `name`, `plugin` | `kafka-connect`: есть | — |
| `debezium_metrics_Connected` | Debezium streaming metrics (JMX exporter) | gauge | `name`, `plugin` | `kafka-connect`: есть | 1 — коннектор подключён к журналу базы. |

<a id="usecase"></a>

## Application use cases

Декоратор application handlers в composition root (docs/observability.md#use-cases). Один histogram на все use cases; `outcome`: success | rejected | error — бизнес-отказ отделён от технического сбоя. `error_type` — закрытый набор slice, пуст при success. Без ID, subject и payload.

| Метрика | Источник | Тип | Labels | Сервис → статус | Примечание |
| --- | --- | --- | --- | --- | --- |
| `nomnom_usecase_duration_seconds` | Use case decorator (`pkg/telemetry`) | histogram | `nomnom_usecase_name`, `nomnom_usecase_kind`, `nomnom_outcome`, `error_type` | `orders`: есть<br>`orders-worker`: есть<br>`orders-consumer`: есть<br>`restaurants`: есть<br>`delivery`: есть<br>`payments`: есть | `kind`: command | query. Rate по `_count` — частота вызовов и доля `rejected`/`error`. |

<a id="uow"></a>

## Unit of Work

Декоратор Unit of Work (ADR 20). Deadlock/serialization — классы retry, а не SQL-панели поверх DB instrumentation.

| Метрика | Источник | Тип | Labels | Сервис → статус | Примечание |
| --- | --- | --- | --- | --- | --- |
| `nomnom_uow_transactions_total` | Unit of Work decorator | counter | `nomnom_outcome` | `orders`: ожидается<br>`orders-worker`: ожидается<br>`orders-consumer`: ожидается<br>`restaurants`: ожидается<br>`delivery`: ожидается<br>`payments`: ожидается | `outcome`: committed | rolled_back | commit_failed. |
| `nomnom_uow_duration_seconds` | Unit of Work decorator | histogram | `nomnom_outcome` | `orders`: ожидается<br>`orders-worker`: ожидается<br>`orders-consumer`: ожидается<br>`restaurants`: ожидается<br>`delivery`: ожидается<br>`payments`: ожидается | — |
| `nomnom_uow_retries_total` | Unit of Work decorator | counter | `nomnom_reason` | `orders`: ожидается<br>`orders-worker`: ожидается<br>`orders-consumer`: ожидается<br>`restaurants`: ожидается<br>`delivery`: ожидается<br>`payments`: ожидается | `reason`: serialization | deadlock | write_conflict | other. |

<a id="fsm"></a>

## Domain Lifecycle (FSM)

Декоратор применения FSM-перехода (ADR 21). Labels допустимы только с закрытым набором значений из FSM-таблицы.

| Метрика | Источник | Тип | Labels | Сервис → статус | Примечание |
| --- | --- | --- | --- | --- | --- |
| `nomnom_fsm_transitions_total` | FSM transition decorator | counter | `nomnom_fsm_aggregate_type`, `nomnom_fsm_from_state`, `nomnom_fsm_to_state`, `nomnom_fsm_trigger`, `nomnom_fsm_result`, `nomnom_fsm_terminal` | `orders`: ожидается<br>`orders-worker`: ожидается<br>`orders-consumer`: ожидается<br>`restaurants`: ожидается<br>`delivery`: ожидается<br>`payments`: ожидается | `result`: applied | rejected (invalid transition); `terminal`: true | false. |

<a id="temporal"></a>

## Temporal

Temporal Go SDK `MetricsHandler` (contrib/opentelemetry, `UseMonotonicCounters`) в `orders-worker` и клиенте `orders-consumer`. Timers — histograms в секундах с границами `telemetry.SecondsBuckets`; без монотонных counters SDK пишет UpDownCounter и в Prometheus counters приходят без `_total`.

| Метрика | Источник | Тип | Labels | Сервис → статус | Примечание |
| --- | --- | --- | --- | --- | --- |
| `temporal_request_total` | Temporal SDK client | counter | `operation`, `namespace` | `orders-worker`: есть<br>`orders-consumer`: есть | `operation="StartWorkflowExecution"` — запуск workflow (клиент `orders-consumer`). |
| `temporal_workflow_completed_total` | Temporal SDK worker | counter | `workflow_type`, `namespace`, `task_queue` | `orders-worker`: есть | — |
| `temporal_workflow_failed_total` | Temporal SDK worker | counter | `workflow_type`, `namespace`, `task_queue` | `orders-worker`: есть | — |
| `temporal_workflow_endtoend_latency_seconds` | Temporal SDK worker | histogram | `workflow_type`, `namespace`, `task_queue` | `orders-worker`: есть | — |
| `temporal_activity_execution_latency_seconds` | Temporal SDK worker | histogram | `activity_type`, `namespace`, `task_queue` | `orders-worker`: есть | — |
| `temporal_activity_execution_failed_total` | Temporal SDK worker | counter | `activity_type`, `namespace`, `task_queue` | `orders-worker`: есть | Каждая неуспешная попытка; retries = попытки сверх первой. |
| `temporal_worker_task_slots_used` | Temporal SDK worker | gauge | `worker_type`, `task_queue` | `orders-worker`: есть | — |
| `temporal_worker_task_slots_available` | Temporal SDK worker | gauge | `worker_type`, `task_queue` | `orders-worker`: есть | — |
| `nomnom_orders_fulfillment_oldest_running_age_seconds` | Polling Temporal Visibility | gauge | — | `orders-worker`: ожидается | Возраст старейшего выполняющегося Order Fulfillment Workflow; SDK его не даёт. |
| `nomnom_orders_compensations_total` | Compensation Activity decorator | counter | `nomnom_compensation_step`, `nomnom_outcome` | `orders-worker`: ожидается | `outcome`: attempted | failed. |

<a id="kitchen"></a>

## Kitchen

Gauges kitchen queue — projection/polling Kitchen Board, не выводятся из counters. Consumer lag ≠ Kitchen Queue Size.

| Метрика | Источник | Тип | Labels | Сервис → статус | Примечание |
| --- | --- | --- | --- | --- | --- |
| `nomnom_kitchen_queue_size` | Kitchen Board projection | gauge | — | `restaurants`: ожидается | — |
| `nomnom_kitchen_queue_oldest_age_seconds` | Kitchen Board projection | gauge | — | `restaurants`: ожидается | — |
| `nomnom_kitchen_preparations_active` | Kitchen Board projection | gauge | — | `restaurants`: ожидается | — |
| `nomnom_kitchen_ready_for_pickup` | Kitchen Board projection | gauge | — | `restaurants`: ожидается | — |
| `nomnom_kitchen_preparation_duration_seconds` | Kitchen use case decorator | histogram | — | `restaurants`: ожидается | — |
| `nomnom_kitchen_delays_total` | Domain Events после commit Unit of Work (ObservedDatabaseUnitOfWork / ObservedOutbox) | counter | `nomnom_reason` | `restaurants`: ожидается | `nomnom_reason` — утверждённый закрытый набор `too_busy | item_unavailable`, остальное `other`. |

<a id="authz"></a>

## Authorization (SpiceDB)

Декоратор порта авторизации (ADR 18). `denied` — бизнес-результат, `error`/`timeout` — технический сбой. Без subject/resource ID.

| Метрика | Источник | Тип | Labels | Сервис → статус | Примечание |
| --- | --- | --- | --- | --- | --- |
| `nomnom_authz_checks_total` | Authorization port decorator | counter | `nomnom_authz_permission`, `nomnom_authz_resource_type`, `nomnom_authz_result` | `orders`: ожидается<br>`orders-worker`: ожидается<br>`orders-consumer`: ожидается<br>`restaurants`: ожидается<br>`delivery`: ожидается<br>`payments`: ожидается | `result`: allowed | denied | error | timeout. |
| `nomnom_authz_check_duration_seconds` | Authorization port decorator | histogram | `nomnom_authz_permission`, `nomnom_authz_resource_type`, `nomnom_authz_result` | `orders`: ожидается<br>`orders-worker`: ожидается<br>`orders-consumer`: ожидается<br>`restaurants`: ожидается<br>`delivery`: ожидается<br>`payments`: ожидается | — |

<a id="provider"></a>

## Payment provider

Клиентский адаптер платёжного провайдера; провайдер не выбран.

| Метрика | Источник | Тип | Labels | Сервис → статус | Примечание |
| --- | --- | --- | --- | --- | --- |
| `nomnom_payments_provider_request_duration_seconds` | Provider gateway adapter | histogram | `nomnom_payment_operation`, `nomnom_outcome` | `payments`: ожидается | `operation`: authorize | capture | refund | cancel; `outcome`: success | declined | error | timeout. `declined` — бизнес-результат. |

<a id="domain"></a>

## Domain metrics

Бизнес-счётчики увеличиваются в точке идемпотентно подтверждённого перехода (docs/observability.md). Финансовые значения и ID не labels. Gauge «currently in state» требует projection/polling и из counters не выводится.

| Метрика | Источник | Тип | Labels | Сервис → статус | Примечание |
| --- | --- | --- | --- | --- | --- |
| `nomnom_orders_placed_total` | Domain Events после commit Unit of Work (ObservedStore / ObservedUnitOfWork) | counter | — | `orders`: есть | — |
| `nomnom_orders_completed_total` | Domain Events после commit Unit of Work (ObservedStore / ObservedUnitOfWork) | counter | — | `orders-worker`: ожидается<br>`orders-consumer`: ожидается | — |
| `nomnom_orders_cancelled_total` | Domain Events после commit Unit of Work (ObservedStore / ObservedUnitOfWork) | counter | `nomnom_reason` | `orders`: ожидается<br>`orders-worker`: ожидается | — |
| `nomnom_orders_failed_total` | Domain Events после commit Unit of Work (ObservedStore / ObservedUnitOfWork) | counter | `nomnom_reason` | `orders-worker`: ожидается | — |
| `nomnom_orders_cancellation_requests_total` | use case decorator | counter | `nomnom_outcome` | `orders`: ожидается | — |
| `nomnom_orders_fulfillment_duration_seconds` | Domain Events после commit Unit of Work (ObservedStore / ObservedUnitOfWork) | histogram | — | `orders-worker`: ожидается | — |
| `nomnom_orders_in_state` | projection/polling | gauge | `nomnom_order_state` | `orders`: ожидается | Требует отдельной projection; PromQL по counters не эмулируется. |
| `nomnom_restaurants_orders_received_total` | Receive Order Request после commit (`created: true`) | counter | — | `restaurants`: ожидается | Пишет `restaurants`: Restaurant Order создаёт только gRPC ReceiveOrderRequest workflow `orders` после оплаты. |
| `nomnom_restaurants_orders_accepted_total` | Domain Events после commit Unit of Work (ObservedDatabaseUnitOfWork / ObservedOutbox) | counter | — | `restaurants`: ожидается | — |
| `nomnom_restaurants_orders_rejected_total` | Domain Events после commit Unit of Work (ObservedDatabaseUnitOfWork / ObservedOutbox) | counter | `nomnom_reason` | `restaurants`: ожидается | `nomnom_reason` — утверждённый закрытый набор `too_busy | item_unavailable | restaurant_closed`, остальное `other`. |
| `nomnom_delivery_created_total` | Domain Events после commit Unit of Work (ObservedOutbox) | counter | — | `delivery`: ожидается | — |
| `nomnom_delivery_assignments_total` | Domain Events после commit Unit of Work (ObservedOutbox) | counter | — | `delivery`: ожидается | — |
| `nomnom_delivery_reassignments_total` | Domain Events после commit Unit of Work (ObservedOutbox) | counter | `nomnom_reason` | `delivery`: ожидается | `nomnom_reason` всегда `other`: у снятия курьера в домене нет причины. |
| `nomnom_delivery_offers_total` | Domain Events после commit Unit of Work (ObservedOutbox) | counter | `nomnom_outcome` | `delivery`: ожидается | `nomnom_outcome`: accepted | rejected | expired. `expired` — pending offer после `expires_at`, закрытый таймером Assignment Workflow (`delivery-worker`, FR-705). |
| `nomnom_delivery_pickups_total` | Domain Events после commit Unit of Work (ObservedOutbox) | counter | — | `delivery`: ожидается | — |
| `nomnom_delivery_completed_total` | Domain Events после commit Unit of Work (ObservedOutbox) | counter | — | `delivery`: ожидается | — |
| `nomnom_delivery_failed_total` | Domain Events после commit Unit of Work (ObservedOutbox) | counter | `nomnom_reason` | `delivery`: ожидается | `nomnom_reason` — каталог `customer_unreachable | address_not_found | no_access | customer_refused | order_damaged | courier_incident | other`. Свободный `detail` — только в логе `delivery.failure_reported` и span, не в метриках. Рост доли `other` — сигнал дополнить каталог. |
| `nomnom_delivery_assignment_latency_seconds` | Domain Events после commit Unit of Work (ObservedOutbox) | histogram | — | `delivery`: ожидается | — |
| `nomnom_delivery_duration_seconds` | Domain Events после commit Unit of Work (ObservedOutbox) | histogram | — | `delivery`: ожидается | — |
| `nomnom_payments_authorizations_total` | Domain Events после commit Unit of Work (ObservedStore / ObservedUnitOfWork) | counter | `nomnom_outcome` | `payments`: ожидается | `nomnom_outcome`: authorized | declined | failed; attempts = сумма. `failed` пока без источника: сбой провайдера оставляет операцию pending без события. |
| `nomnom_payments_captures_total` | Domain Events после commit Unit of Work (ObservedStore / ObservedUnitOfWork) | counter | `nomnom_outcome` | `payments`: ожидается | `nomnom_outcome`: captured | declined | failed. `declined` — отказ провайдера (бизнес-результат), `failed` — технический сбой. |
| `nomnom_payments_refunds_total` | Domain Events после commit Unit of Work (ObservedStore / ObservedUnitOfWork) | counter | `nomnom_outcome` | `payments`: ожидается | `nomnom_outcome`: refunded | declined | failed. `declined` — отказ провайдера (бизнес-результат), `failed` — технический сбой. |

<a id="pods"></a>

## Pods (Kubernetes)

Alloy: cAdvisor kubelet через API server proxy и kube-state-metrics (только pods прикладных неймспейсов), `job="infra/kubernetes"`. Метрики Prometheus, без `target_info`: сервис выбирается `namespace` и префиксом `pod`. Row Pods в overview каждого сервиса и в Platform / Overview.

| Метрика | Источник | Тип | Labels | Сервис → статус | Примечание |
| --- | --- | --- | --- | --- | --- |
| `container_cpu_usage_seconds_total` | cAdvisor kubelet | counter | `namespace`, `pod`, `container` | `orders`: есть<br>`orders-worker`: ожидается<br>`orders-consumer`: есть<br>`restaurants`: есть<br>`delivery`: есть<br>`payments`: есть<br>`bff`: ожидается | Только контейнеры: pod-level cgroup и pause отбрасываются в Alloy. |
| `container_memory_working_set_bytes` | cAdvisor kubelet | gauge | `namespace`, `pod`, `container` | `orders`: есть<br>`orders-worker`: ожидается<br>`orders-consumer`: есть<br>`restaurants`: есть<br>`delivery`: есть<br>`payments`: есть<br>`bff`: ожидается | Working set — то, что учитывает OOM killer и eviction. |
| `container_network_receive_bytes_total` | cAdvisor kubelet | counter | `namespace`, `pod` | `orders`: есть<br>`orders-worker`: ожидается<br>`orders-consumer`: есть<br>`restaurants`: есть<br>`delivery`: есть<br>`payments`: есть<br>`bff`: ожидается | Уровень pod, без `lo`. |
| `container_network_transmit_bytes_total` | cAdvisor kubelet | counter | `namespace`, `pod` | `orders`: есть<br>`orders-worker`: ожидается<br>`orders-consumer`: есть<br>`restaurants`: есть<br>`delivery`: есть<br>`payments`: есть<br>`bff`: ожидается | Уровень pod, без `lo`. |
| `kube_pod_status_ready` | kube-state-metrics v2.20 | gauge | `namespace`, `pod`, `condition` | `orders`: есть<br>`orders-worker`: ожидается<br>`orders-consumer`: есть<br>`restaurants`: есть<br>`delivery`: есть<br>`payments`: есть<br>`bff`: ожидается | В Alloy остаётся только `condition="true"`. |
| `kube_pod_container_status_restarts_total` | kube-state-metrics v2.20 | counter | `namespace`, `pod`, `container` | `orders`: есть<br>`orders-worker`: ожидается<br>`orders-consumer`: есть<br>`restaurants`: есть<br>`delivery`: есть<br>`payments`: есть<br>`bff`: ожидается | — |
| `kube_pod_container_status_waiting_reason` | kube-state-metrics v2.20 | gauge | `namespace`, `pod`, `container`, `reason` | `orders`: есть<br>`orders-worker`: ожидается<br>`orders-consumer`: есть<br>`restaurants`: есть<br>`delivery`: есть<br>`payments`: есть<br>`bff`: ожидается | `CrashLoopBackOff`, `ImagePullBackOff`, `CreateContainerConfigError` и т. п. |

<a id="spicedb"></a>

## SpiceDB (сервер)

Alloy scrape `:9090` pod SpiceDB, `job="infra/spicedb"`. Серверная сторона; клиентская — row Authorization сервисов. Platform / Overview.

| Метрика | Источник | Тип | Labels | Сервис → статус | Примечание |
| --- | --- | --- | --- | --- | --- |
| `grpc_server_handled_total` | SpiceDB go-grpc-prometheus | counter | `grpc_service`, `grpc_method`, `grpc_code`, `grpc_type` | `spicedb`: есть | Health-check `grpc.health.v1.Health` отбрасывается в Alloy. |
| `grpc_server_handling_seconds` | SpiceDB go-grpc-prometheus | histogram | `grpc_service`, `grpc_method`, `grpc_type` | `spicedb`: есть | — |
| `spicedb_v1_check_permissionship_total` | SpiceDB | counter | `method`, `permissionship` | `spicedb`: есть | `permissionship`: HAS_PERMISSION | NO_PERMISSION | CONDITIONAL_PERMISSION — решение, не ошибка. |
| `spicedb_dispatch_client_check_total` | SpiceDB dispatcher | counter | — | `spicedb`: есть | — |
| `spicedb_dispatch_client_check_from_cache_total` | SpiceDB dispatcher | counter | — | `spicedb`: есть | — |
| `spicedb_datastore_query_latency` | SpiceDB datastore | histogram | `operation`, `query_shape` | `spicedb`: есть | Доставляются только `_sum`/`_count` (средняя latency): buckets — ~100 series. |
| `process_cpu_seconds_total` | Prometheus Go client | counter | — | `spicedb`: есть | — |
| `process_resident_memory_bytes` | Prometheus Go client | gauge | — | `spicedb`: есть | — |

<a id="postgresql"></a>

## PostgreSQL (сервер)

Alloy `prometheus.exporter.postgres` по владельцу (ADR 8), `job="infra/postgresql"`, label `owner`; `stat_database` и размер — только база владельца. Клиентская сторона — row Dependencies сервисов. Platform / Overview.

| Метрика | Источник | Тип | Labels | Сервис → статус | Примечание |
| --- | --- | --- | --- | --- | --- |
| `pg_up` | postgres_exporter | gauge | `owner` | `postgresql`: есть | — |
| `pg_replication_slot_slot_current_wal_lsn` | postgres_exporter `replication_slot` | gauge | `owner`, `slot_name`, `slot_type` | `postgresql`: есть | Имена и labels сняты scrape экспортера на стенде 2026-10-26 (Debezium 3.7.0.Final, JMX exporter 1.7.0, Alloy 1.21.2). |
| `pg_replication_slot_slot_confirmed_flush_lsn` | postgres_exporter `replication_slot` | gauge | `owner`, `slot_name`, `slot_type` | `postgresql`: есть | current − confirmed_flush — WAL, который слот Debezium держит на диске (ADR 37). |
| `pg_replication_slot_slot_is_active` | postgres_exporter `replication_slot` | gauge | `owner`, `slot_name`, `slot_type` | `postgresql`: есть | 0 — коннектор не читает слот, WAL копится. |
| `pg_stat_database_numbackends` | postgres_exporter `stat_database` | gauge | `owner`, `datname` | `postgresql`: есть | — |
| `pg_stat_database_xact_commit` | postgres_exporter `stat_database` | counter | `owner`, `datname` | `postgresql`: есть | Counter без `_total` (имя postgres_exporter). |
| `pg_stat_database_xact_rollback` | postgres_exporter `stat_database` | counter | `owner`, `datname` | `postgresql`: есть | — |
| `pg_stat_database_blks_hit` | postgres_exporter `stat_database` | counter | `owner`, `datname` | `postgresql`: есть | — |
| `pg_stat_database_blks_read` | postgres_exporter `stat_database` | counter | `owner`, `datname` | `postgresql`: есть | — |
| `pg_stat_database_deadlocks` | postgres_exporter `stat_database` | counter | `owner`, `datname` | `postgresql`: есть | — |
| `pg_stat_database_conflicts` | postgres_exporter `stat_database` | counter | `owner`, `datname` | `postgresql`: есть | — |
| `pg_stat_database_temp_bytes` | postgres_exporter `stat_database` | counter | `owner`, `datname` | `postgresql`: есть | Запросы, вылезшие за `work_mem`. |
| `pg_database_size_bytes` | postgres_exporter `database` | gauge | `owner`, `datname` | `postgresql`: есть | — |

<a id="valkey"></a>

## Valkey (сервер)

Alloy `prometheus.exporter.redis` по владельцу (ADR 43), `job="infra/valkey"`, label `owner`; ACL-пользователь `exporter` только с INFO, CONFIG GET и служебными командами, без доступа к ключам. Клиентская сторона — row Cache сервиса. Platform / Overview.

| Метрика | Источник | Тип | Labels | Сервис → статус | Примечание |
| --- | --- | --- | --- | --- | --- |
| `redis_up` | redis_exporter (Alloy v1.21) | gauge | `owner` | `valkey`: ожидается | 0 — экспортер не подключился к Valkey владельца. |
| `redis_connected_clients` | redis_exporter `INFO clients` | gauge | `owner` | `valkey`: ожидается | — |
| `redis_memory_used_bytes` | redis_exporter `INFO memory` | gauge | `owner` | `valkey`: ожидается | — |
| `redis_memory_max_bytes` | redis_exporter `CONFIG GET maxmemory` | gauge | `owner` | `valkey`: ожидается | `maxmemory`; у kind — 64 MiB, `allkeys-lru`. |
| `redis_commands_processed_total` | redis_exporter `INFO stats` | counter | `owner` | `valkey`: ожидается | — |
| `redis_keyspace_hits_total` | redis_exporter `INFO stats` | counter | `owner` | `valkey`: ожидается | Попадания по всем GET сервера, включая чтение поколения страниц; hit ratio кеша по чтениям — `nomnom_cache_lookups_total` сервиса. |
| `redis_keyspace_misses_total` | redis_exporter `INFO stats` | counter | `owner` | `valkey`: ожидается | — |
| `redis_evicted_keys_total` | redis_exporter `INFO stats` | counter | `owner` | `valkey`: ожидается | Вытеснение по `maxmemory`: кеш мал для рабочего набора. |
| `redis_expired_keys_total` | redis_exporter `INFO stats` | counter | `owner` | `valkey`: ожидается | — |
| `redis_db_keys` | redis_exporter `INFO keyspace` | gauge | `owner`, `db` | `valkey`: ожидается | — |

<a id="certificates"></a>

## Certificates (cert-manager)

Alloy scrape `:9402` controller cert-manager, `job="infra/cert-manager"`: все `Certificate` кластера, `namespace`/`name` — неймспейс и имя ресурса. Row Certificates в Platform / Overview и в overview контекстов, у которых есть свои сертификаты (BFF: `bff-assertion-key`).

| Метрика | Источник | Тип | Labels | Сервис → статус | Примечание |
| --- | --- | --- | --- | --- | --- |
| `certmanager_certificate_not_after_timestamp_seconds` | cert-manager v1.21 controller | gauge | `namespace`, `name`, `issuer_name`, `issuer_kind` | `cert-manager`: есть | NotAfter выпущенного сертификата, Unix seconds. |
| `certmanager_certificate_renewal_timestamp_seconds` | cert-manager v1.21 controller | gauge | `namespace`, `name`, `issuer_name`, `issuer_kind` | `cert-manager`: есть | Плановое продление (`renewBefore`); прошедшее время при Ready — продление не удаётся. |
| `certmanager_certificate_ready_status` | cert-manager v1.21 controller | gauge | `namespace`, `name`, `condition`, `issuer_name`, `issuer_kind` | `cert-manager`: есть | В Alloy остаётся только `condition="True"`: 0 — Certificate не Ready. |


<!-- Сгенерировано ops/grafana (make grafana-generate). Не редактировать вручную: источник — ops/grafana/inventory/inventory.go. -->

# Инвентарь метрик

Инвентарь фиксирует метрики, на которых строятся overview-dashboards сервисов
([docs/observability.md](../observability.md#dashboards)). Панель строится только на метриках
со статусом «есть»; для остальных генератор рисует TODO-панель со ссылкой на раздел этого документа.

| Статус | Значение |
| --- | --- |
| есть | Метрика экспортируется сервисом **и** доставляется в Mimir существующим pipeline. |
| ожидается | Метрика принята архитектурой (semconv, ADR, решение по Watermill), но не экспортируется или не доставляется. |

Имена приведены в виде Mimir после OTLP-трансляции (точки → подчёркивания, unit suffix, `_total` для
монотонных counters). Для histogram указано базовое имя; запросы используют `_bucket`/`_count`/`_sum`.

## Состояние на дату генерации

Метрик в инвентаре: 84; пар «метрика × сервис» со статусом «есть»: 0.

Ни один backend-сервис пока не подключает OpenTelemetry SDK: OTel-пакеты в `orders` и BFF — только
транзитивные зависимости, технический порт `metrics` отдаёт health, Helm values содержат `OTEL_*` лишь в
комментариях, а Alloy, Mimir и Grafana в репозитории не развёрнуты. Поэтому все панели overview сейчас — TODO.
Встроенные метрики Watermill приняты как базовый слой, но `PrometheusMetricsBuilder` в `orders-consumer` не
подключён — статус «ожидается» до появления wiring.

<a id="resource"></a>

## Resource и идентичность

OTel resource в Mimir: `job="nomnom/<service.name>"`, `instance=<service.instance.id>`, остальные resource attributes — в `target_info`. Promotion resource attributes не используется (docs/observability.md), поэтому environment и version фильтруются join с `target_info`.

| Метрика | Источник | Тип | Labels | Сервис → статус | Примечание |
| --- | --- | --- | --- | --- | --- |
| `target_info` | OTel resource → Mimir OTLP | gauge | `job`, `instance`, `deployment_environment_name`, `service_version` | `orders`: ожидается<br>`orders-worker`: ожидается<br>`orders-consumer`: ожидается<br>`restaurants`: ожидается<br>`delivery`: ожидается<br>`payments`: ожидается<br>`bff`: ожидается | Нужна для variables `environment`/`version`/`service_instance`, фильтра environment и deployment annotations. |

<a id="rpc"></a>

## gRPC (server и client через internal gateway)

`otelgrpc` / grpc-node / tonic / grpcio OTel instrumentation. Semconv RPC ещё в статусе development: актуальное имя `rpc.server.call.duration` (s) заменит `rpc.server.duration` (ms) при закреплении версии SDK; генератор меняется в одном месте.

| Метрика | Источник | Тип | Labels | Сервис → статус | Примечание |
| --- | --- | --- | --- | --- | --- |
| `rpc_server_duration_milliseconds` | `rpc.server.duration` | histogram | `rpc_system`, `rpc_service`, `rpc_method`, `rpc_grpc_status_code` | `orders`: ожидается<br>`restaurants`: ожидается<br>`delivery`: ожидается<br>`payments`: ожидается | — |
| `rpc_client_duration_milliseconds` | `rpc.client.duration` | histogram | `rpc_system`, `rpc_service`, `rpc_method`, `rpc_grpc_status_code` | `orders-worker`: ожидается<br>`bff`: ожидается | Вызовы через internal gateway (ADR 15); SpiceDB — в row Authorization. |

<a id="graphql"></a>

## GraphQL / HTTP (BFF, Cosmo Router)

Встроенная telemetry Cosmo Router. Router экспортирует метрики по умолчанию, но OTLP exporter к Alloy в `config.yaml` не настроен и Alloy не развёрнут — метрики не доставляются.

| Метрика | Источник | Тип | Labels | Сервис → статус | Примечание |
| --- | --- | --- | --- | --- | --- |
| `router_http_requests_total` | Cosmo `router.http.requests` | counter | `wg_operation_type`, `wg_operation_name`, `wg_subgraph_name`, `http_status_code` | `bff`: ожидается | `wg_operation_name` ограничен persisted operations клиента. |
| `router_http_requests_error_total` | Cosmo `router.http.requests.error` | counter | `wg_operation_type`, `wg_operation_name`, `wg_subgraph_name`, `http_status_code` | `bff`: ожидается | GraphQL errors в ответе 200. |
| `router_http_request_duration_milliseconds` | Cosmo `router.http.request.duration_milliseconds` | histogram | `wg_operation_type`, `wg_operation_name`, `wg_subgraph_name`, `http_status_code` | `bff`: ожидается | Без `wg_subgraph_name` — клиентский запрос, с ним — вызов subgraph (BFF plugin → internal gateway). |

<a id="runtime"></a>

## Runtime

Go: `go.opentelemetry.io/contrib/instrumentation/runtime` (semconv runtime Go). Node.js: `@opentelemetry/instrumentation-runtime-node`. Python: `opentelemetry-instrumentation-system-metrics`. Rust: semconv process metrics; метрики tokio runtime не имеют semconv и не инвентаризированы.

| Метрика | Источник | Тип | Labels | Сервис → статус | Примечание |
| --- | --- | --- | --- | --- | --- |
| `process_cpu_time_seconds_total` | `process.cpu.time` | counter | `cpu_mode` | `orders`: ожидается<br>`orders-worker`: ожидается<br>`orders-consumer`: ожидается<br>`restaurants`: ожидается<br>`delivery`: ожидается<br>`payments`: ожидается | Go: `go.opentelemetry.io/contrib/instrumentation/host`. |
| `process_memory_usage_bytes` | `process.memory.usage` | gauge | — | `restaurants`: ожидается<br>`delivery`: ожидается<br>`payments`: ожидается | Для Go не экспортируется; память Go-ролей — `go_memory_used_bytes`. |
| `process_thread_count` | `process.thread.count` | gauge | — | `delivery`: ожидается<br>`payments`: ожидается | Python: worker model gRPC thread pool. |
| `go_memory_used_bytes` | `go.memory.used` | gauge | `go_memory_type` | `orders`: ожидается<br>`orders-worker`: ожидается<br>`orders-consumer`: ожидается | — |
| `go_memory_allocated_bytes_total` | `go.memory.allocated` | counter | — | `orders`: ожидается<br>`orders-worker`: ожидается<br>`orders-consumer`: ожидается | — |
| `go_memory_gc_goal_bytes` | `go.memory.gc.goal` | gauge | — | `orders`: ожидается<br>`orders-worker`: ожидается<br>`orders-consumer`: ожидается | — |
| `go_goroutine_count` | `go.goroutine.count` | gauge | — | `orders`: ожидается<br>`orders-worker`: ожидается<br>`orders-consumer`: ожидается | — |
| `go_processor_limit` | `go.processor.limit` | gauge | — | `orders`: ожидается<br>`orders-worker`: ожидается<br>`orders-consumer`: ожидается | — |
| `v8js_memory_heap_used_bytes` | `v8js.memory.heap.used` | gauge | `v8js_heap_space_name` | `restaurants`: ожидается | — |
| `v8js_memory_heap_limit_bytes` | `v8js.memory.heap.limit` | gauge | `v8js_heap_space_name` | `restaurants`: ожидается | — |
| `nodejs_eventloop_delay_p99_seconds` | `nodejs.eventloop.delay.p99` | gauge | — | `restaurants`: ожидается | — |
| `v8js_gc_duration_seconds` | `v8js.gc.duration` | histogram | `v8js_gc_type` | `restaurants`: ожидается | — |
| `cpython_gc_collections_total` | `cpython.gc.collections` | counter | `cpython_gc_generation` | `payments`: ожидается | — |

<a id="db"></a>

## Database client (PostgreSQL, MongoDB)

Semconv `db.client.*`: `pgx` + otelpgx в `orders`, MongoDB Node.js driver instrumentation в `restaurants`. Хранилища `delivery` и `payments` не выбраны (ADR 8) — метрик для них нет. Серверные метрики PostgreSQL/MongoDB — отдельные community dashboards, не overview.

| Метрика | Источник | Тип | Labels | Сервис → статус | Примечание |
| --- | --- | --- | --- | --- | --- |
| `db_client_operation_duration_seconds` | `db.client.operation.duration` | histogram | `db_system_name`, `db_operation_name`, `db_collection_name`, `error_type` | `orders`: ожидается<br>`orders-worker`: ожидается<br>`orders-consumer`: ожидается<br>`restaurants`: ожидается | — |
| `db_client_connection_count` | `db.client.connection.count` | gauge | `db_client_connection_pool_name`, `db_client_connection_state` | `orders`: ожидается<br>`orders-worker`: ожидается<br>`orders-consumer`: ожидается<br>`restaurants`: ожидается | — |
| `db_client_connection_max` | `db.client.connection.max` | gauge | `db_client_connection_pool_name` | `orders`: ожидается<br>`orders-worker`: ожидается<br>`orders-consumer`: ожидается<br>`restaurants`: ожидается | — |
| `db_client_connection_wait_time_seconds` | `db.client.connection.wait_time` | histogram | `db_client_connection_pool_name` | `orders`: ожидается<br>`orders-worker`: ожидается<br>`orders-consumer`: ожидается<br>`restaurants`: ожидается | — |

<a id="watermill"></a>

## Kafka: встроенные метрики Watermill

`components/metrics.PrometheusMetricsBuilder`, namespace `watermill`, subsystem — deployable role (`consumer`, `outbox_forwarder`). Экспорт через OTel Prometheus bridge в общий OTLP pipeline. Референс панелей — Grafana dashboard 9777, отдельный dashboard не создаётся.

| Метрика | Источник | Тип | Labels | Сервис → статус | Примечание |
| --- | --- | --- | --- | --- | --- |
| `watermill_consumer_subscriber_messages_received_total` | `PrometheusMetricsBuilder` subsystem `consumer` | counter | `acked`, `handler_name`, `subscriber_name` | `orders-consumer`: ожидается | Решение принято; builder в `orders-consumer` ещё не подключён (`pkg/messaging/consumer.go`). |
| `watermill_consumer_handler_execution_time_seconds` | `PrometheusMetricsBuilder` subsystem `consumer` | histogram | `handler_name`, `success` | `orders-consumer`: ожидается | Как выше. |
| `watermill_consumer_publish_time_seconds` | `PrometheusMetricsBuilder` subsystem `consumer` | histogram | `success`, `handler_name`, `publisher_name` | `orders-consumer`: ожидается | Publisher consumer-роли — PoisonQueue в `<stream>.dlq`. |
| `watermill_outbox_forwarder_publish_time_seconds` | `PrometheusMetricsBuilder` subsystem `outbox_forwarder` | histogram | `success`, `handler_name`, `publisher_name` | `orders`: ожидается<br>`orders-worker`: ожидается<br>`orders-consumer`: ожидается | Forwarder-роль `orders` ещё не выделена; метрика привязана к service.name процесса, где он запущен. |

<a id="messaging-otel"></a>

## Kafka: semconv messaging (не-Go контексты)

Kafka-клиенты TypeScript, Rust и Python не выбраны; при выборе используется OTel messaging semconv.

| Метрика | Источник | Тип | Labels | Сервис → статус | Примечание |
| --- | --- | --- | --- | --- | --- |
| `messaging_client_consumed_messages_total` | `messaging.client.consumed.messages` | counter | `messaging_system`, `messaging_destination_name`, `messaging_consumer_group_name`, `error_type` | `restaurants`: ожидается<br>`delivery`: ожидается<br>`payments`: ожидается | — |
| `messaging_client_sent_messages_total` | `messaging.client.sent.messages` | counter | `messaging_system`, `messaging_destination_name`, `error_type` | `restaurants`: ожидается<br>`delivery`: ожидается<br>`payments`: ожидается | — |
| `messaging_client_operation_duration_seconds` | `messaging.client.operation.duration` | histogram | `messaging_system`, `messaging_operation_name`, `messaging_destination_name`, `error_type` | `restaurants`: ожидается<br>`delivery`: ожидается<br>`payments`: ожидается | — |

<a id="messaging"></a>

## Kafka: собственный OTel middleware и consumer lag

Middleware в composition root поверх Watermill (и эквивалент в других runtime): то, чего нет во встроенных метриках. Порядок: OTel/metrics → Retry (transient) → PoisonQueue (permanent) → handler. Labels только `handler_name`, `event_type`, `stream`, `error_class`; без `event_id`/`aggregate_id`. Consumer lag — Alloy `prometheus.exporter.kafka`, не Watermill.

| Метрика | Источник | Тип | Labels | Сервис → статус | Примечание |
| --- | --- | --- | --- | --- | --- |
| `nomnom_messaging_process_duration_seconds` | OTel middleware | histogram | `handler_name`, `event_type`, `stream`, `error_class` | `orders-consumer`: ожидается<br>`restaurants`: ожидается<br>`delivery`: ожидается<br>`payments`: ожидается | Длительность обработки по `event_type`; `error_class` пуст при успехе. |
| `nomnom_messaging_retry_attempts_total` | OTel middleware | counter | `handler_name`, `event_type`, `stream`, `error_class` | `orders-consumer`: ожидается<br>`restaurants`: ожидается<br>`delivery`: ожидается<br>`payments`: ожидается | Только transient: business errors не ретраятся. |
| `nomnom_messaging_poison_messages_total` | OTel middleware вокруг PoisonQueue | counter | `handler_name`, `event_type`, `stream`, `error_class` | `orders-consumer`: ожидается<br>`restaurants`: ожидается<br>`delivery`: ожидается<br>`payments`: ожидается | Сообщения, отправленные в `<stream>.dlq`. |
| `nomnom_messaging_message_age_seconds` | OTel middleware | histogram | `handler_name`, `event_type`, `stream` | `orders-consumer`: ожидается<br>`restaurants`: ожидается<br>`delivery`: ожидается<br>`payments`: ожидается | `now − occurred_at` в момент обработки. |
| `nomnom_inbox_duplicates_total` | OTel middleware / Inbox adapter | counter | `handler_name`, `event_type`, `stream` | `orders-consumer`: ожидается<br>`restaurants`: ожидается<br>`delivery`: ожидается<br>`payments`: ожидается | Конфликт уникальности `(consumer, event_id)` = duplicate. |
| `kafka_consumergroup_lag` | Alloy `prometheus.exporter.kafka` | gauge | `consumergroup`, `topic`, `partition` | `orders-consumer`: ожидается<br>`restaurants`: ожидается<br>`delivery`: ожидается<br>`payments`: ожидается | Серверная метрика с job exporter'а; фильтр по `consumergroup` сервиса, без target_info join. |

<a id="outbox"></a>

## Outbox

SQL-метрики Forwarder-роли (опрос таблицы Outbox), не publish-декоратор.

| Метрика | Источник | Тип | Labels | Сервис → статус | Примечание |
| --- | --- | --- | --- | --- | --- |
| `nomnom_outbox_pending_messages` | SQL-метрика Forwarder | gauge | `stream` | `orders`: ожидается<br>`orders-worker`: ожидается<br>`orders-consumer`: ожидается<br>`restaurants`: ожидается<br>`delivery`: ожидается<br>`payments`: ожидается | — |
| `nomnom_outbox_oldest_pending_age_seconds` | SQL-метрика Forwarder | gauge | `stream` | `orders`: ожидается<br>`orders-worker`: ожидается<br>`orders-consumer`: ожидается<br>`restaurants`: ожидается<br>`delivery`: ожидается<br>`payments`: ожидается | — |
| `nomnom_outbox_publish_retries_total` | Forwarder | counter | `stream`, `error_class` | `orders`: ожидается<br>`orders-worker`: ожидается<br>`orders-consumer`: ожидается<br>`restaurants`: ожидается<br>`delivery`: ожидается<br>`payments`: ожидается | — |

<a id="uow"></a>

## Unit of Work

Декоратор Unit of Work (ADR 20). Deadlock/serialization — классы retry, а не SQL-панели поверх DB instrumentation.

| Метрика | Источник | Тип | Labels | Сервис → статус | Примечание |
| --- | --- | --- | --- | --- | --- |
| `nomnom_uow_transactions_total` | Unit of Work decorator | counter | `outcome` | `orders`: ожидается<br>`orders-worker`: ожидается<br>`orders-consumer`: ожидается<br>`restaurants`: ожидается<br>`delivery`: ожидается<br>`payments`: ожидается | `outcome`: committed | rolled_back | commit_failed. |
| `nomnom_uow_duration_seconds` | Unit of Work decorator | histogram | `outcome` | `orders`: ожидается<br>`orders-worker`: ожидается<br>`orders-consumer`: ожидается<br>`restaurants`: ожидается<br>`delivery`: ожидается<br>`payments`: ожидается | — |
| `nomnom_uow_retries_total` | Unit of Work decorator | counter | `reason` | `orders`: ожидается<br>`orders-worker`: ожидается<br>`orders-consumer`: ожидается<br>`restaurants`: ожидается<br>`delivery`: ожидается<br>`payments`: ожидается | `reason`: serialization | deadlock | write_conflict | other. |

<a id="fsm"></a>

## Domain Lifecycle (FSM)

Декоратор применения FSM-перехода (ADR 21). Labels допустимы только с закрытым набором значений из FSM-таблицы.

| Метрика | Источник | Тип | Labels | Сервис → статус | Примечание |
| --- | --- | --- | --- | --- | --- |
| `nomnom_fsm_transitions_total` | FSM transition decorator | counter | `aggregate_type`, `from_state`, `to_state`, `trigger`, `result`, `terminal` | `orders`: ожидается<br>`orders-worker`: ожидается<br>`orders-consumer`: ожидается<br>`restaurants`: ожидается<br>`delivery`: ожидается<br>`payments`: ожидается | `result`: applied | rejected (invalid transition); `terminal`: true | false. |

<a id="temporal"></a>

## Temporal

Temporal Go SDK `MetricsHandler` (contrib/opentelemetry) в `orders-worker` и клиенте `orders`. Суффикс единицы timers зависит от handler и проверяется при подключении.

| Метрика | Источник | Тип | Labels | Сервис → статус | Примечание |
| --- | --- | --- | --- | --- | --- |
| `temporal_request_total` | Temporal SDK client | counter | `operation`, `namespace` | `orders`: ожидается<br>`orders-worker`: ожидается | `operation="StartWorkflowExecution"` — запуск workflow. |
| `temporal_workflow_completed_total` | Temporal SDK worker | counter | `workflow_type`, `namespace`, `task_queue` | `orders-worker`: ожидается | — |
| `temporal_workflow_failed_total` | Temporal SDK worker | counter | `workflow_type`, `namespace`, `task_queue` | `orders-worker`: ожидается | — |
| `temporal_workflow_endtoend_latency_seconds` | Temporal SDK worker | histogram | `workflow_type`, `namespace`, `task_queue` | `orders-worker`: ожидается | — |
| `temporal_activity_execution_latency_seconds` | Temporal SDK worker | histogram | `activity_type`, `namespace`, `task_queue` | `orders-worker`: ожидается | — |
| `temporal_activity_execution_failed_total` | Temporal SDK worker | counter | `activity_type`, `namespace`, `task_queue` | `orders-worker`: ожидается | Каждая неуспешная попытка; retries = попытки сверх первой. |
| `temporal_worker_task_slots_used` | Temporal SDK worker | gauge | `worker_type`, `task_queue` | `orders-worker`: ожидается | — |
| `temporal_worker_task_slots_available` | Temporal SDK worker | gauge | `worker_type`, `task_queue` | `orders-worker`: ожидается | — |
| `nomnom_orders_fulfillment_oldest_running_age_seconds` | Polling Temporal Visibility | gauge | — | `orders-worker`: ожидается | Возраст старейшего выполняющегося Order Fulfillment Workflow; SDK его не даёт. |
| `nomnom_orders_compensations_total` | Compensation Activity decorator | counter | `step`, `outcome` | `orders-worker`: ожидается | `outcome`: attempted | failed. |

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
| `nomnom_kitchen_delays_total` | Kitchen use case decorator | counter | `reason` | `restaurants`: ожидается | — |

<a id="authz"></a>

## Authorization (SpiceDB)

Декоратор порта авторизации (ADR 18). `denied` — бизнес-результат, `error`/`timeout` — технический сбой. Без subject/resource ID.

| Метрика | Источник | Тип | Labels | Сервис → статус | Примечание |
| --- | --- | --- | --- | --- | --- |
| `nomnom_authz_checks_total` | Authorization port decorator | counter | `permission`, `resource_type`, `result` | `orders`: ожидается<br>`orders-worker`: ожидается<br>`orders-consumer`: ожидается<br>`restaurants`: ожидается<br>`delivery`: ожидается<br>`payments`: ожидается | `result`: allowed | denied | error | timeout. |
| `nomnom_authz_check_duration_seconds` | Authorization port decorator | histogram | `permission`, `resource_type`, `result` | `orders`: ожидается<br>`orders-worker`: ожидается<br>`orders-consumer`: ожидается<br>`restaurants`: ожидается<br>`delivery`: ожидается<br>`payments`: ожидается | — |

<a id="provider"></a>

## Payment provider

Клиентский адаптер платёжного провайдера; провайдер не выбран.

| Метрика | Источник | Тип | Labels | Сервис → статус | Примечание |
| --- | --- | --- | --- | --- | --- |
| `nomnom_payments_provider_request_duration_seconds` | Provider gateway adapter | histogram | `operation`, `outcome` | `payments`: ожидается | `operation`: authorize | capture | refund | cancel; `outcome`: success | declined | error | timeout. `declined` — бизнес-результат. |

<a id="domain"></a>

## Domain metrics

Бизнес-счётчики увеличиваются в точке идемпотентно подтверждённого перехода (docs/observability.md). Финансовые значения и ID не labels. Gauge «currently in state» требует projection/polling и из counters не выводится.

| Метрика | Источник | Тип | Labels | Сервис → статус | Примечание |
| --- | --- | --- | --- | --- | --- |
| `nomnom_orders_placed_total` | use case decorator | counter | — | `orders`: ожидается | — |
| `nomnom_orders_completed_total` | use case decorator | counter | — | `orders-worker`: ожидается<br>`orders-consumer`: ожидается | — |
| `nomnom_orders_cancelled_total` | use case decorator | counter | `reason` | `orders`: ожидается<br>`orders-worker`: ожидается | — |
| `nomnom_orders_failed_total` | use case decorator | counter | `reason` | `orders-worker`: ожидается | — |
| `nomnom_orders_cancellation_requests_total` | use case decorator | counter | `outcome` | `orders`: ожидается | — |
| `nomnom_orders_fulfillment_duration_seconds` | use case decorator | histogram | — | `orders-worker`: ожидается | — |
| `nomnom_orders_in_state` | projection/polling | gauge | `state` | `orders`: ожидается | Требует отдельной projection; PromQL по counters не эмулируется. |
| `nomnom_restaurants_orders_received_total` | use case decorator | counter | — | `restaurants`: ожидается | — |
| `nomnom_restaurants_orders_accepted_total` | use case decorator | counter | — | `restaurants`: ожидается | — |
| `nomnom_restaurants_orders_rejected_total` | use case decorator | counter | `reason` | `restaurants`: ожидается | — |
| `nomnom_delivery_created_total` | use case decorator | counter | — | `delivery`: ожидается | — |
| `nomnom_delivery_assignments_total` | use case decorator | counter | — | `delivery`: ожидается | — |
| `nomnom_delivery_reassignments_total` | use case decorator | counter | `reason` | `delivery`: ожидается | — |
| `nomnom_delivery_offers_total` | use case decorator | counter | `outcome` | `delivery`: ожидается | `outcome`: accepted | rejected | expired. |
| `nomnom_delivery_pickups_total` | use case decorator | counter | — | `delivery`: ожидается | — |
| `nomnom_delivery_completed_total` | use case decorator | counter | — | `delivery`: ожидается | — |
| `nomnom_delivery_failed_total` | use case decorator | counter | `reason` | `delivery`: ожидается | — |
| `nomnom_delivery_assignment_latency_seconds` | use case decorator | histogram | — | `delivery`: ожидается | — |
| `nomnom_delivery_duration_seconds` | use case decorator | histogram | — | `delivery`: ожидается | — |
| `nomnom_payments_authorizations_total` | use case decorator | counter | `outcome` | `payments`: ожидается | `outcome`: authorized | declined | failed; attempts = сумма. |
| `nomnom_payments_captures_total` | use case decorator | counter | `outcome` | `payments`: ожидается | `outcome`: captured | failed. |
| `nomnom_payments_refunds_total` | use case decorator | counter | `outcome` | `payments`: ожидается | `outcome`: refunded | failed. |


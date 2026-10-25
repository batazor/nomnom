<!-- Сгенерировано ops/grafana (make grafana-generate). Не редактировать вручную. -->

# Orders / Overview

Overview-dashboard bounded context `orders`: один на сервис, отвечает на вопросы incident — работает ли сервис, выросли ли ошибки и latency, какая зависимость деградировала, есть ли backlog, что с Inbox/Outbox и бизнес-потоком, где trace/log/profile.

| | |
| --- | --- |
| UID | `nomnom-orders-overview` |
| Файл | [`dashboards/overview.json`](dashboards/overview.json) |
| Схема | `dashboard.grafana.app/v2` (Grafana 13) |
| Доставка | Git Sync, repository [`ops/grafana/git-sync/orders.yaml`](../../../../ops/grafana/git-sync/orders.yaml) |
| Генератор | [`ops/grafana`](../../../../ops/grafana/README.md), функция `overview.ServiceOverview` |
| service.name | `orders`, `orders-worker`, `orders-consumer` |

JSON — артефакт генератора: правки вносятся в `ops/grafana/overview/services.go` или в инвентарь, затем `make grafana-generate`. CI (`make grafana-check`) падает при расхождении.

## Data sources

Выбираются dashboard variables по типу plugin, без UID окружения: `metrics` (Prometheus API Mimir), `logs` (Loki), `traces` (Tempo), `profiles` (Pyroscope).

## Variables

| Variable | Источник | Назначение |
| --- | --- | --- |
| `environment` | `target_info` → `deployment_environment_name` | Фильтр окружения через join с `target_info`. |
| `service` | список service.name из генератора (`orders`, `orders-worker`, `orders-consumer`) | `job=~"nomnom/(${service:pipe})"`. |
| `version` | `target_info` → `service_version` | Сравнение версий во время выкатки. |
| `service_instance` | `target_info` → `instance` (`service.instance.id`) | Диагностика экземпляра. |
| `worker` (section, row Temporal) | `orders-worker` | Temporal worker фильтруется отдельно от API. |

`namespace` не добавлен: `k8s.namespace.name` не входит в принятые resource attributes. High-cardinality variables (`order_id`, `user_id`, `payment_id`, `event_id`, `workflow_id`) запрещены — для них traces и logs.

## Rows

### Service health

| Панель | Статус | Недостающие метрики |
| --- | --- | --- |
| Request rate by entry | live | [`rpc_server_duration_milliseconds`](../../../../docs/observability/metric-inventory.md#rpc), [`temporal_activity_execution_latency_seconds`](../../../../docs/observability/metric-inventory.md#temporal) |
| Errors by class | live | [`rpc_server_duration_milliseconds`](../../../../docs/observability/metric-inventory.md#rpc), [`temporal_activity_execution_failed_total`](../../../../docs/observability/metric-inventory.md#temporal) |
| Technical error ratio | live | [`rpc_server_duration_milliseconds`](../../../../docs/observability/metric-inventory.md#rpc) |
| Latency p50 / p95 / p99 | TODO | [`rpc_server_duration_milliseconds`](../../../../docs/observability/metric-inventory.md#rpc) |
| CPU | TODO | [`process_cpu_time_seconds_total`](../../../../docs/observability/metric-inventory.md#runtime) |
| Memory | TODO | [`go_memory_used_bytes`](../../../../docs/observability/metric-inventory.md#runtime) |

### Runtime

| Панель | Статус | Недостающие метрики |
| --- | --- | --- |
| Heap and runtime memory | TODO | [`go_memory_gc_goal_bytes`](../../../../docs/observability/metric-inventory.md#runtime), [`go_memory_used_bytes`](../../../../docs/observability/metric-inventory.md#runtime) |
| Allocation rate | TODO | [`go_memory_allocated_bytes_total`](../../../../docs/observability/metric-inventory.md#runtime) |
| Goroutines | TODO | [`go_goroutine_count`](../../../../docs/observability/metric-inventory.md#runtime) |
| GOMAXPROCS | TODO | [`go_processor_limit`](../../../../docs/observability/metric-inventory.md#runtime) |

### Dependencies

| Панель | Статус | Недостающие метрики |
| --- | --- | --- |
| gRPC via internal gateway: rate | TODO | [`rpc_client_duration_milliseconds`](../../../../docs/observability/metric-inventory.md#rpc) |
| gRPC via internal gateway: technical errors | TODO | [`rpc_client_duration_milliseconds`](../../../../docs/observability/metric-inventory.md#rpc) |
| gRPC via internal gateway: p95 | TODO | [`rpc_client_duration_milliseconds`](../../../../docs/observability/metric-inventory.md#rpc) |
| postgresql: operations | TODO | [`db_client_operation_duration_seconds`](../../../../docs/observability/metric-inventory.md#db) |
| postgresql: p95 by operation | TODO | [`db_client_operation_duration_seconds`](../../../../docs/observability/metric-inventory.md#db) |
| postgresql: pool | TODO | [`db_client_connection_count`](../../../../docs/observability/metric-inventory.md#db), [`db_client_connection_max`](../../../../docs/observability/metric-inventory.md#db) |
| postgresql: pool wait p95 | TODO | [`db_client_connection_wait_time_seconds`](../../../../docs/observability/metric-inventory.md#db) |

### Kafka

| Панель | Статус | Недостающие метрики |
| --- | --- | --- |
| Consumed | live | — |
| Produced (Outbox Forwarder) | TODO | [`watermill_outbox_forwarder_publish_time_seconds`](../../../../docs/observability/metric-inventory.md#watermill) |
| Processing failures | live | — |
| Consumer lag | TODO | [`kafka_consumergroup_lag`](../../../../docs/observability/metric-inventory.md#messaging) |
| Retries and poison (DLQ) | live | — |
| Processing duration p95 by event_type | live | — |
| Message age at processing p95 | live | — |
| Inbox: processed / duplicates | live | — |
| Outbox pending | TODO | [`nomnom_outbox_pending_messages`](../../../../docs/observability/metric-inventory.md#outbox) |
| Outbox oldest pending age | TODO | [`nomnom_outbox_oldest_pending_age_seconds`](../../../../docs/observability/metric-inventory.md#outbox) |
| Outbox publish retries | TODO | [`nomnom_outbox_publish_retries_total`](../../../../docs/observability/metric-inventory.md#outbox) |

### Use cases

| Панель | Статус | Недостающие метрики |
| --- | --- | --- |
| Calls by outcome | TODO | [`nomnom_usecase_duration_seconds`](../../../../docs/observability/metric-inventory.md#usecase) |
| Rejections by use case | TODO | [`nomnom_usecase_duration_seconds`](../../../../docs/observability/metric-inventory.md#usecase) |
| Errors by use case | TODO | [`nomnom_usecase_duration_seconds`](../../../../docs/observability/metric-inventory.md#usecase) |
| Duration p95 by use case | TODO | [`nomnom_usecase_duration_seconds`](../../../../docs/observability/metric-inventory.md#usecase) |

### Transactions

| Панель | Статус | Недостающие метрики |
| --- | --- | --- |
| Transactions by outcome | TODO | [`nomnom_uow_transactions_total`](../../../../docs/observability/metric-inventory.md#uow) |
| Transaction duration | TODO | [`nomnom_uow_duration_seconds`](../../../../docs/observability/metric-inventory.md#uow) |
| Retries by conflict | TODO | [`nomnom_uow_retries_total`](../../../../docs/observability/metric-inventory.md#uow) |

### Domain Lifecycle

| Панель | Статус | Недостающие метрики |
| --- | --- | --- |
| Transitions | TODO | [`nomnom_fsm_transitions_total`](../../../../docs/observability/metric-inventory.md#fsm) |
| Invalid transitions | TODO | [`nomnom_fsm_transitions_total`](../../../../docs/observability/metric-inventory.md#fsm) |
| Terminal transitions | TODO | [`nomnom_fsm_transitions_total`](../../../../docs/observability/metric-inventory.md#fsm) |

### Domain metrics

| Панель | Статус | Недостающие метрики |
| --- | --- | --- |
| Orders: placed / completed / cancelled / failed | TODO | [`nomnom_orders_cancelled_total`](../../../../docs/observability/metric-inventory.md#domain), [`nomnom_orders_completed_total`](../../../../docs/observability/metric-inventory.md#domain), [`nomnom_orders_failed_total`](../../../../docs/observability/metric-inventory.md#domain), [`nomnom_orders_placed_total`](../../../../docs/observability/metric-inventory.md#domain) |
| Cancellation requests | TODO | [`nomnom_orders_cancellation_requests_total`](../../../../docs/observability/metric-inventory.md#domain) |
| Fulfillment duration | TODO | [`nomnom_orders_fulfillment_duration_seconds`](../../../../docs/observability/metric-inventory.md#domain) |
| Orders currently in state | TODO | [`nomnom_orders_in_state`](../../../../docs/observability/metric-inventory.md#domain) |

### Temporal

| Панель | Статус | Недостающие метрики |
| --- | --- | --- |
| Workflows started | TODO | [`target_info`](../../../../docs/observability/metric-inventory.md#resource), [`temporal_request_total`](../../../../docs/observability/metric-inventory.md#temporal) |
| Workflows completed / failed | TODO | [`target_info`](../../../../docs/observability/metric-inventory.md#resource), [`temporal_workflow_completed_total`](../../../../docs/observability/metric-inventory.md#temporal), [`temporal_workflow_failed_total`](../../../../docs/observability/metric-inventory.md#temporal) |
| Workflow end-to-end latency p95 | TODO | [`target_info`](../../../../docs/observability/metric-inventory.md#resource), [`temporal_workflow_endtoend_latency_seconds`](../../../../docs/observability/metric-inventory.md#temporal) |
| Oldest running fulfillment | TODO | [`nomnom_orders_fulfillment_oldest_running_age_seconds`](../../../../docs/observability/metric-inventory.md#temporal), [`target_info`](../../../../docs/observability/metric-inventory.md#resource) |
| Activities executed / failed attempts | TODO | [`target_info`](../../../../docs/observability/metric-inventory.md#resource), [`temporal_activity_execution_failed_total`](../../../../docs/observability/metric-inventory.md#temporal), [`temporal_activity_execution_latency_seconds`](../../../../docs/observability/metric-inventory.md#temporal) |
| Activity latency p95 | TODO | [`target_info`](../../../../docs/observability/metric-inventory.md#resource), [`temporal_activity_execution_latency_seconds`](../../../../docs/observability/metric-inventory.md#temporal) |
| Worker task slots | TODO | [`target_info`](../../../../docs/observability/metric-inventory.md#resource), [`temporal_worker_task_slots_available`](../../../../docs/observability/metric-inventory.md#temporal), [`temporal_worker_task_slots_used`](../../../../docs/observability/metric-inventory.md#temporal) |
| Compensations | TODO | [`nomnom_orders_compensations_total`](../../../../docs/observability/metric-inventory.md#temporal), [`target_info`](../../../../docs/observability/metric-inventory.md#resource) |

### Authorization

| Панель | Статус | Недостающие метрики |
| --- | --- | --- |
| Checks by result | TODO | [`nomnom_authz_checks_total`](../../../../docs/observability/metric-inventory.md#authz) |
| Denied by permission | TODO | [`nomnom_authz_checks_total`](../../../../docs/observability/metric-inventory.md#authz) |
| Errors and timeouts | TODO | [`nomnom_authz_checks_total`](../../../../docs/observability/metric-inventory.md#authz) |
| Check latency | TODO | [`nomnom_authz_check_duration_seconds`](../../../../docs/observability/metric-inventory.md#authz) |

Живых панелей: 9 из 54. TODO-панель показывает недостающие метрики и целевой PromQL; она становится живой после смены статуса в инвентаре и регенерации.

## Переходы

- **Logs** — Explore Loki по `service_name`, `deployment_environment_name`, `service_version` и интервалу dashboard. Переход log → trace — derived field `trace_id` data source Loki.
- **Traces** — Explore Tempo (TraceQL) по сервису и окружению: все, ошибки (`status=error`); панели ошибок и latency содержат data links на ошибочные и медленные traces.
- **Profiles** — Explore Pyroscope (CPU) по service/environment/интервалу. Trace → profile (Span Profiles, только CPU) поддерживается runtime и включается в data source Tempo после проверки end-to-end.
- **Exemplars** — выключены: не подтверждено, что SDK histograms, Alloy и Mimir сохраняют exemplars (`inventory.ExemplarsEnabled`).
- **Deployments** — annotation по смене `service_version` в `target_info`; появляется, когда `target_info` получает статус «есть».
- **Серверные метрики зависимостей** — ссылки на community dashboards в шапке; панели не дублируются.

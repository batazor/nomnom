<!-- Сгенерировано ops/grafana (make grafana-generate). Не редактировать вручную. -->

# Restaurants / Overview

Overview-dashboard bounded context `restaurants`: один на сервис, отвечает на вопросы incident — работает ли сервис, выросли ли ошибки и latency, какая зависимость деградировала, есть ли backlog, что с Inbox/Outbox и бизнес-потоком, где trace/log/profile.

| | |
| --- | --- |
| UID | `nomnom-restaurants-overview` |
| Файл | [`dashboards/overview.json`](dashboards/overview.json) |
| Схема | `dashboard.grafana.app/v2` (Grafana 13) |
| Доставка | Git Sync, repository [`ops/grafana/git-sync/restaurants.yaml`](../../../../ops/grafana/git-sync/restaurants.yaml) |
| Генератор | [`ops/grafana`](../../../../ops/grafana/README.md), функция `overview.ServiceOverview` |
| service.name | `restaurants`, `restaurants-consumer` |

JSON — артефакт генератора: правки вносятся в `ops/grafana/overview/services.go` или в инвентарь, затем `make grafana-generate`. CI (`make grafana-check`) падает при расхождении.

## SLO

Цели за скользящие 30d, решение — [ADR 40](../../../../docs/adr/0040-service-slo-and-burn-rate-alerts.md). Business rejection бюджет не тратит.

| SLI | Цель | Error budget |
| --- | --- | --- |
| Availability | 99.5% | 0.5% |
| Latency ≤ 0.5s | 95% | 5% |

## Data sources

Выбираются dashboard variables по типу plugin, без UID окружения: `metrics` (Prometheus API Grafana Cloud), `logs` (Loki), `traces` (Tempo), `profiles` (Pyroscope).

## Variables

| Variable | Источник | Назначение |
| --- | --- | --- |
| `environment` | `target_info` → `deployment_environment_name` | Фильтр окружения через join с `target_info`. |
| `service` | список service.name из генератора (`restaurants`, `restaurants-consumer`) | `job=~"nomnom/(${service:pipe})"`. |
| `version` | `target_info` → `service_version` | Сравнение версий во время выкатки. |
| `service_instance` | `target_info` → `instance` (`service.instance.id`) | Диагностика экземпляра. |

`namespace` не добавлен: `k8s.namespace.name` не входит в принятые resource attributes. High-cardinality variables (`order_id`, `user_id`, `payment_id`, `event_id`, `workflow_id`) запрещены — для них traces и logs.

## Rows

### Service health

| Панель | Статус | Недостающие метрики |
| --- | --- | --- |
| Request rate by entry | live | [`nomnom_messaging_process_duration_seconds`](../../../../docs/observability/metric-inventory.md#messaging) |
| Errors by class | live | [`nomnom_messaging_poison_messages_total`](../../../../docs/observability/metric-inventory.md#messaging), [`nomnom_messaging_process_duration_seconds`](../../../../docs/observability/metric-inventory.md#messaging) |
| Technical error ratio | live | [`nomnom_messaging_process_duration_seconds`](../../../../docs/observability/metric-inventory.md#messaging) |
| Latency p50 / p95 / p99 | live | — |
| CPU | live | — |
| Memory | live | — |

### SLO

| Панель | Статус | Недостающие метрики |
| --- | --- | --- |
| Availability: SLI / SLO 99.5% | live | — |
| Availability: error budget remaining (30d) | live | — |
| Availability: burn rate | live | — |
| Latency ≤ 0.5s: SLI / SLO 95% | live | — |
| Latency ≤ 0.5s: error budget remaining (30d) | live | — |
| Latency ≤ 0.5s: burn rate | live | — |

### Runtime

| Панель | Статус | Недостающие метрики |
| --- | --- | --- |
| V8 heap used / limit | TODO | [`v8js_memory_heap_limit_bytes`](../../../../docs/observability/metric-inventory.md#runtime), [`v8js_memory_heap_used_bytes`](../../../../docs/observability/metric-inventory.md#runtime) |
| Event loop delay p99 | TODO | [`nodejs_eventloop_delay_p99_seconds`](../../../../docs/observability/metric-inventory.md#runtime) |
| GC duration p95 | TODO | [`v8js_gc_duration_seconds`](../../../../docs/observability/metric-inventory.md#runtime) |

### Pods

| Панель | Статус | Недостающие метрики |
| --- | --- | --- |
| Ready pods | live | — |
| Restarts | live | — |
| Waiting containers by reason | live | — |
| CPU by pod | live | — |
| Memory working set by pod | live | — |
| Network by pod | live | — |

### Dependencies

| Панель | Статус | Недостающие метрики |
| --- | --- | --- |
| mongodb: operations | live | — |
| mongodb: p95 by operation | live | — |
| mongodb: pool connections | live | [`db_client_connection_idle_min`](../../../../docs/observability/metric-inventory.md#db), [`db_client_connection_max`](../../../../docs/observability/metric-inventory.md#db) |
| mongodb: pool saturation | TODO | [`db_client_connection_max`](../../../../docs/observability/metric-inventory.md#db) |
| mongodb: pending checkouts and timeouts | TODO | [`db_client_connection_pending_requests`](../../../../docs/observability/metric-inventory.md#db), [`db_client_connection_timeouts_total`](../../../../docs/observability/metric-inventory.md#db) |
| mongodb: pool wait / use / create p95 | TODO | [`db_client_connection_create_time_seconds`](../../../../docs/observability/metric-inventory.md#db), [`db_client_connection_use_time_seconds`](../../../../docs/observability/metric-inventory.md#db), [`db_client_connection_wait_time_seconds`](../../../../docs/observability/metric-inventory.md#db) |
| mongodb: pool cleared | TODO | [`nomnom_db_client_connection_pool_clears_total`](../../../../docs/observability/metric-inventory.md#db) |

### Cache

| Панель | Статус | Недостающие метрики |
| --- | --- | --- |
| Hit ratio by cache | TODO | [`nomnom_cache_lookups_total`](../../../../docs/observability/metric-inventory.md#cache) |
| Lookups by result | TODO | [`nomnom_cache_lookups_total`](../../../../docs/observability/metric-inventory.md#cache) |
| Bypass share | TODO | [`nomnom_cache_lookups_total`](../../../../docs/observability/metric-inventory.md#cache) |
| Refreshes by events | TODO | [`nomnom_cache_refreshes_total`](../../../../docs/observability/metric-inventory.md#cache) |
| Valkey command errors | TODO | [`nomnom_cache_errors_total`](../../../../docs/observability/metric-inventory.md#cache) |

### Kafka

| Панель | Статус | Недостающие метрики |
| --- | --- | --- |
| Consumed | TODO | [`messaging_client_consumed_messages_total`](../../../../docs/observability/metric-inventory.md#messaging-otel) |
| Produced (Outbox, Debezium) | live | — |
| Processing failures | TODO | [`nomnom_messaging_process_duration_seconds`](../../../../docs/observability/metric-inventory.md#messaging) |
| Consumer lag | TODO | [`kafka_consumergroup_lag`](../../../../docs/observability/metric-inventory.md#messaging) |
| Retries and poison (DLQ) | TODO | [`nomnom_messaging_poison_messages_total`](../../../../docs/observability/metric-inventory.md#messaging), [`nomnom_messaging_retry_attempts_total`](../../../../docs/observability/metric-inventory.md#messaging) |
| Processing duration p95 by event_type | TODO | [`nomnom_messaging_process_duration_seconds`](../../../../docs/observability/metric-inventory.md#messaging) |
| Message age at processing p95 | TODO | [`nomnom_messaging_message_age_seconds`](../../../../docs/observability/metric-inventory.md#messaging) |
| Inbox: processed / duplicates | TODO | [`nomnom_inbox_duplicates_total`](../../../../docs/observability/metric-inventory.md#messaging), [`nomnom_messaging_process_duration_seconds`](../../../../docs/observability/metric-inventory.md#messaging) |
| Outbox CDC lag | live | — |
| Outbox connector tasks | live | — |

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
| Restaurant orders: received / accepted / rejected | TODO | [`nomnom_restaurants_orders_accepted_total`](../../../../docs/observability/metric-inventory.md#domain), [`nomnom_restaurants_orders_received_total`](../../../../docs/observability/metric-inventory.md#domain), [`nomnom_restaurants_orders_rejected_total`](../../../../docs/observability/metric-inventory.md#domain) |
| Rejections by reason | TODO | [`nomnom_restaurants_orders_rejected_total`](../../../../docs/observability/metric-inventory.md#domain) |

### Kitchen

| Панель | Статус | Недостающие метрики |
| --- | --- | --- |
| Kitchen queue size | TODO | [`nomnom_kitchen_queue_size`](../../../../docs/observability/metric-inventory.md#kitchen) |
| Oldest queued order age | TODO | [`nomnom_kitchen_queue_oldest_age_seconds`](../../../../docs/observability/metric-inventory.md#kitchen) |
| Active preparations / ready for pickup | TODO | [`nomnom_kitchen_preparations_active`](../../../../docs/observability/metric-inventory.md#kitchen), [`nomnom_kitchen_ready_for_pickup`](../../../../docs/observability/metric-inventory.md#kitchen) |
| Preparation duration | TODO | [`nomnom_kitchen_preparation_duration_seconds`](../../../../docs/observability/metric-inventory.md#kitchen) |
| Delays | TODO | [`nomnom_kitchen_delays_total`](../../../../docs/observability/metric-inventory.md#kitchen) |

### Authorization

| Панель | Статус | Недостающие метрики |
| --- | --- | --- |
| Checks by result | TODO | [`nomnom_authz_checks_total`](../../../../docs/observability/metric-inventory.md#authz) |
| Denied by permission | TODO | [`nomnom_authz_checks_total`](../../../../docs/observability/metric-inventory.md#authz) |
| Errors and timeouts | TODO | [`nomnom_authz_checks_total`](../../../../docs/observability/metric-inventory.md#authz) |
| Check latency | TODO | [`nomnom_authz_check_duration_seconds`](../../../../docs/observability/metric-inventory.md#authz) |

Живых панелей: 24 из 60. TODO-панель показывает недостающие метрики и целевой PromQL; она становится живой после смены статуса в инвентаре и регенерации.

## Alerts

Rule group `restaurants` в folder `nomnom` ([`alerts/rules.json`](alerts/rules.json)), push — `make grafana-cloud-push`. Правило создаётся, только когда его метрики «есть»: alert без данных ничего не защищает. Пороги — стартовые для тестового окружения; SLO burn rate — по целям [ADR 40](../../../../docs/adr/0040-service-slo-and-burn-rate-alerts.md).

| Alert | Severity | Статус | Недостающие метрики |
| --- | --- | --- | --- |
| Restaurants: gRPC technical errors | critical | активно | — |
| Restaurants: gRPC p95 latency | warning | активно | — |
| Restaurants: messages sent to DLQ | warning | ждёт метрик | [`nomnom_messaging_poison_messages_total`](../../../../docs/observability/metric-inventory.md#messaging) |
| Restaurants: unresolved dead letters | warning | ждёт метрик | [`kafka_consumergroup_current_offset`](../../../../docs/observability/metric-inventory.md#messaging), [`kafka_topic_partition_current_offset`](../../../../docs/observability/metric-inventory.md#messaging), [`kafka_topic_partition_oldest_offset`](../../../../docs/observability/metric-inventory.md#messaging) |
| Restaurants: cache bypassed | warning | ждёт метрик | [`nomnom_cache_lookups_total`](../../../../docs/observability/metric-inventory.md#cache) |
| Restaurants: mongodb pool saturated | warning | ждёт метрик | [`db_client_connection_max`](../../../../docs/observability/metric-inventory.md#db) |
| Restaurants: mongodb pool checkouts queued | warning | ждёт метрик | [`db_client_connection_pending_requests`](../../../../docs/observability/metric-inventory.md#db) |
| Restaurants: mongodb pool wait p95 | warning | ждёт метрик | [`db_client_connection_wait_time_seconds`](../../../../docs/observability/metric-inventory.md#db) |
| Restaurants: mongodb pool checkout timeouts | warning | ждёт метрик | [`db_client_connection_timeouts_total`](../../../../docs/observability/metric-inventory.md#db) |
| Restaurants: mongodb pool cleared | warning | ждёт метрик | [`nomnom_db_client_connection_pool_clears_total`](../../../../docs/observability/metric-inventory.md#db) |
| Restaurants: Availability SLO burn rate 1h | critical | активно | — |
| Restaurants: Availability SLO burn rate 6h | warning | активно | — |
| Restaurants: Latency ≤ 0.5s SLO burn rate 1h | critical | активно | — |
| Restaurants: Latency ≤ 0.5s SLO burn rate 6h | warning | активно | — |

## Переходы

- **Logs** — Explore Loki по `service_name`, `deployment_environment_name`, `service_version` и интервалу dashboard. Переход log → trace — derived field `trace_id` data source Loki.
- **Traces** — Explore Tempo (TraceQL) по сервису и окружению: все, ошибки (`status=error`); у серий панелей ошибок и latency — data link «Traces этой серии»: spans с теми же attributes, что labels серии (`rpc.method`, `rpc.response.status_code`, `db.operation.name`, `nomnom.usecase.name`, `error.type`), у latency — не быстрее значения точки.
- **Profiles** — Explore Pyroscope (CPU) по service/environment/интервалу. Span Profiles для этого runtime не поддерживаются: только корреляция service/version/time.
- **Exemplars** — выключены: не подтверждено, что SDK histograms, Alloy и Grafana Cloud сохраняют exemplars (`inventory.ExemplarsEnabled`).
- **Deployments** — annotation по смене `service_version` в `target_info`; появляется, когда `target_info` получает статус «есть».
- **Серверные метрики зависимостей** — ссылки на community dashboards в шапке; панели не дублируются.

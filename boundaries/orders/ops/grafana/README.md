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

## SLO

Цели за скользящие 30d, решение — [ADR 40](../../../../docs/adr/0040-service-slo-and-burn-rate-alerts.md). Business rejection бюджет не тратит.

| SLI | Цель | Error budget |
| --- | --- | --- |
| Availability | 99.9% | 0.1% |
| Latency ≤ 0.25s | 95% | 5% |

## Data sources

Выбираются dashboard variables по типу plugin, без UID окружения: `metrics` (Prometheus API Grafana Cloud), `logs` (Loki), `traces` (Tempo), `profiles` (Pyroscope).

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
| Request rate by entry | live | — |
| Errors by class | live | — |
| Technical error ratio | live | — |
| Latency p50 / p95 / p99 | live | — |
| CPU | live | — |
| Memory | live | — |

### SLO

| Панель | Статус | Недостающие метрики |
| --- | --- | --- |
| Availability: SLI / SLO 99.9% | live | — |
| Availability: error budget remaining (30d) | live | — |
| Availability: burn rate | live | — |
| Latency ≤ 0.25s: SLI / SLO 95% | live | — |
| Latency ≤ 0.25s: error budget remaining (30d) | live | — |
| Latency ≤ 0.25s: burn rate | live | — |

### Runtime

| Панель | Статус | Недостающие метрики |
| --- | --- | --- |
| Heap and runtime memory | live | [`go_memory_limit_bytes`](../../../../docs/observability/metric-inventory.md#runtime) |
| Allocation rate | live | — |
| Goroutines | live | — |
| GOMAXPROCS | live | — |

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
| gRPC via internal gateway: rate | live | — |
| gRPC via internal gateway: technical errors | live | — |
| gRPC via internal gateway: p95 | live | — |
| postgresql: operations | live | — |
| postgresql: p95 by operation | live | — |
| postgresql: pool connections | live | [`db_client_connection_idle_min`](../../../../docs/observability/metric-inventory.md#db), [`db_client_connection_max`](../../../../docs/observability/metric-inventory.md#db) |
| postgresql: pool saturation | TODO | [`db_client_connection_max`](../../../../docs/observability/metric-inventory.md#db) |
| postgresql: pending checkouts and timeouts | TODO | [`db_client_connection_pending_requests`](../../../../docs/observability/metric-inventory.md#db), [`db_client_connection_timeouts_total`](../../../../docs/observability/metric-inventory.md#db) |
| postgresql: pool wait / use / create p95 | TODO | [`db_client_connection_create_time_seconds`](../../../../docs/observability/metric-inventory.md#db), [`db_client_connection_use_time_seconds`](../../../../docs/observability/metric-inventory.md#db), [`db_client_connection_wait_time_seconds`](../../../../docs/observability/metric-inventory.md#db) |

### Kafka

| Панель | Статус | Недостающие метрики |
| --- | --- | --- |
| Consumed | live | — |
| Produced (Outbox, Debezium) | live | — |
| Processing failures | live | — |
| Consumer lag | TODO | [`kafka_consumergroup_lag`](../../../../docs/observability/metric-inventory.md#messaging) |
| Retries and poison (DLQ) | live | — |
| Processing duration p95 by event_type | live | — |
| Message age at processing p95 | live | — |
| Inbox: processed / duplicates | live | — |
| Outbox CDC lag | live | — |
| Outbox connector tasks | live | — |

### Use cases

| Панель | Статус | Недостающие метрики |
| --- | --- | --- |
| Calls by outcome | live | — |
| Rejections by use case | live | — |
| Errors by use case | live | — |
| Duration p95 by use case | live | — |

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
| Orders: placed / completed / cancelled / failed | live | [`nomnom_orders_cancelled_total`](../../../../docs/observability/metric-inventory.md#domain), [`nomnom_orders_completed_total`](../../../../docs/observability/metric-inventory.md#domain), [`nomnom_orders_failed_total`](../../../../docs/observability/metric-inventory.md#domain) |
| Cancellation requests | TODO | [`nomnom_orders_cancellation_requests_total`](../../../../docs/observability/metric-inventory.md#domain) |
| Fulfillment duration | TODO | [`nomnom_orders_fulfillment_duration_seconds`](../../../../docs/observability/metric-inventory.md#domain) |
| Orders currently in state | TODO | [`nomnom_orders_in_state`](../../../../docs/observability/metric-inventory.md#domain) |

### Order funnel

| Панель | Статус | Недостающие метрики |
| --- | --- | --- |
| Funnel: orders reaching the stage | live | [`nomnom_fsm_transitions_total`](../../../../docs/observability/metric-inventory.md#fsm) |
| Funnel: % of placed | live | [`nomnom_fsm_transitions_total`](../../../../docs/observability/metric-inventory.md#fsm) |
| Lost: state before cancellation | TODO | [`nomnom_fsm_transitions_total`](../../../../docs/observability/metric-inventory.md#fsm) |
| Lost: reason | TODO | [`nomnom_orders_cancelled_total`](../../../../docs/observability/metric-inventory.md#domain), [`nomnom_orders_failed_total`](../../../../docs/observability/metric-inventory.md#domain) |

### Temporal

| Панель | Статус | Недостающие метрики |
| --- | --- | --- |
| Workflows started | live | — |
| Workflows completed / failed | live | — |
| Workflow end-to-end latency p95 | live | — |
| Oldest running fulfillment | TODO | [`nomnom_orders_fulfillment_oldest_running_age_seconds`](../../../../docs/observability/metric-inventory.md#temporal) |
| Activities executed / failed attempts | live | — |
| Activity latency p95 | live | — |
| Worker task slots | live | — |
| Compensations | TODO | [`nomnom_orders_compensations_total`](../../../../docs/observability/metric-inventory.md#temporal) |

### Authorization

| Панель | Статус | Недостающие метрики |
| --- | --- | --- |
| Checks by result | TODO | [`nomnom_authz_checks_total`](../../../../docs/observability/metric-inventory.md#authz) |
| Denied by permission | TODO | [`nomnom_authz_checks_total`](../../../../docs/observability/metric-inventory.md#authz) |
| Errors and timeouts | TODO | [`nomnom_authz_checks_total`](../../../../docs/observability/metric-inventory.md#authz) |
| Check latency | TODO | [`nomnom_authz_check_duration_seconds`](../../../../docs/observability/metric-inventory.md#authz) |

Живых панелей: 50 из 71. TODO-панель показывает недостающие метрики и целевой PromQL; она становится живой после смены статуса в инвентаре и регенерации.

## Alerts

Rule group `orders` в folder `nomnom` ([`alerts/rules.json`](alerts/rules.json)), push — `make grafana-cloud-push`. Правило создаётся, только когда его метрики «есть»: alert без данных ничего не защищает. Пороги — стартовые для тестового окружения; SLO burn rate — по целям [ADR 40](../../../../docs/adr/0040-service-slo-and-burn-rate-alerts.md).

| Alert | Severity | Статус | Недостающие метрики |
| --- | --- | --- | --- |
| Orders: gRPC technical errors | critical | активно | — |
| Orders: gRPC p95 latency | warning | активно | — |
| Orders: neighbour calls failing | warning | активно | — |
| Orders: use case errors | warning | активно | — |
| Orders: messages sent to DLQ | warning | активно | — |
| Orders: unresolved dead letters | warning | ждёт метрик | [`kafka_consumergroup_current_offset`](../../../../docs/observability/metric-inventory.md#messaging), [`kafka_topic_partition_current_offset`](../../../../docs/observability/metric-inventory.md#messaging), [`kafka_topic_partition_oldest_offset`](../../../../docs/observability/metric-inventory.md#messaging) |
| Orders: postgresql pool saturated | warning | ждёт метрик | [`db_client_connection_max`](../../../../docs/observability/metric-inventory.md#db) |
| Orders: postgresql pool checkouts queued | warning | ждёт метрик | [`db_client_connection_pending_requests`](../../../../docs/observability/metric-inventory.md#db) |
| Orders: postgresql pool wait p95 | warning | ждёт метрик | [`db_client_connection_wait_time_seconds`](../../../../docs/observability/metric-inventory.md#db) |
| Orders: postgresql pool checkout timeouts | warning | ждёт метрик | [`db_client_connection_timeouts_total`](../../../../docs/observability/metric-inventory.md#db) |
| Orders: Availability SLO burn rate 1h | critical | активно | — |
| Orders: Availability SLO burn rate 6h | warning | активно | — |
| Orders: Latency ≤ 0.25s SLO burn rate 1h | critical | активно | — |
| Orders: Latency ≤ 0.25s SLO burn rate 6h | warning | активно | — |

## Переходы

- **Logs** — Explore Loki по `service_name`, `deployment_environment_name`, `service_version` и интервалу dashboard. Переход log → trace — derived field `trace_id` data source Loki.
- **Traces** — Explore Tempo (TraceQL) по сервису и окружению: все, ошибки (`status=error`); у серий панелей ошибок и latency — data link «Traces этой серии»: spans с теми же attributes, что labels серии (`rpc.method`, `rpc.response.status_code`, `db.operation.name`, `nomnom.usecase.name`, `error.type`), у latency — не быстрее значения точки.
- **Profiles** — Explore Pyroscope (CPU) по service/environment/интервалу. Trace → profile (Span Profiles, только CPU) поддерживается runtime и включается в data source Tempo после проверки end-to-end.
- **Exemplars** — выключены: не подтверждено, что SDK histograms, Alloy и Grafana Cloud сохраняют exemplars (`inventory.ExemplarsEnabled`).
- **Deployments** — annotation по смене `service_version` в `target_info`; появляется, когда `target_info` получает статус «есть».
- **Серверные метрики зависимостей** — ссылки на community dashboards в шапке; панели не дублируются.

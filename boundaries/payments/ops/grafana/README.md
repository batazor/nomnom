<!-- Сгенерировано ops/grafana (make grafana-generate). Не редактировать вручную. -->

# Payments / Overview

Overview-dashboard bounded context `payments`: один на сервис, отвечает на вопросы incident — работает ли сервис, выросли ли ошибки и latency, какая зависимость деградировала, есть ли backlog, что с Inbox/Outbox и бизнес-потоком, где trace/log/profile.

| | |
| --- | --- |
| UID | `nomnom-payments-overview` |
| Файл | [`dashboards/overview.json`](dashboards/overview.json) |
| Схема | `dashboard.grafana.app/v2` (Grafana 13) |
| Доставка | Git Sync, repository [`ops/grafana/git-sync/payments.yaml`](../../../../ops/grafana/git-sync/payments.yaml) |
| Генератор | [`ops/grafana`](../../../../ops/grafana/README.md), функция `overview.ServiceOverview` |
| service.name | `payments` |

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
| `service` | список service.name из генератора (`payments`) | `job=~"nomnom/(${service:pipe})"`. |
| `version` | `target_info` → `service_version` | Сравнение версий во время выкатки. |
| `service_instance` | `target_info` → `instance` (`service.instance.id`) | Диагностика экземпляра. |

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
| GC collections | TODO | [`cpython_gc_collections_total`](../../../../docs/observability/metric-inventory.md#runtime) |
| Threads (gRPC worker pool) | TODO | [`process_thread_count`](../../../../docs/observability/metric-inventory.md#runtime) |
| CPU by mode | live | — |

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
| postgresql: operations | TODO | [`db_client_operation_duration_seconds`](../../../../docs/observability/metric-inventory.md#db) |
| postgresql: p95 by operation | TODO | [`db_client_operation_duration_seconds`](../../../../docs/observability/metric-inventory.md#db) |
| postgresql: pool connections | live | [`db_client_connection_idle_min`](../../../../docs/observability/metric-inventory.md#db), [`db_client_connection_max`](../../../../docs/observability/metric-inventory.md#db) |
| postgresql: pool saturation | TODO | [`db_client_connection_max`](../../../../docs/observability/metric-inventory.md#db) |
| postgresql: pending checkouts and timeouts | TODO | [`db_client_connection_pending_requests`](../../../../docs/observability/metric-inventory.md#db), [`db_client_connection_timeouts_total`](../../../../docs/observability/metric-inventory.md#db) |
| postgresql: pool wait / use / create p95 | TODO | [`db_client_connection_create_time_seconds`](../../../../docs/observability/metric-inventory.md#db), [`db_client_connection_use_time_seconds`](../../../../docs/observability/metric-inventory.md#db), [`db_client_connection_wait_time_seconds`](../../../../docs/observability/metric-inventory.md#db) |
| Payment provider: requests | TODO | [`nomnom_payments_provider_request_duration_seconds`](../../../../docs/observability/metric-inventory.md#provider) |
| Payment provider: errors and timeouts | TODO | [`nomnom_payments_provider_request_duration_seconds`](../../../../docs/observability/metric-inventory.md#provider) |
| Payment provider: p95 | TODO | [`nomnom_payments_provider_request_duration_seconds`](../../../../docs/observability/metric-inventory.md#provider) |

### Kafka

| Панель | Статус | Недостающие метрики |
| --- | --- | --- |
| Produced (Outbox, Debezium) | live | — |
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
| Transitions | live | — |
| Invalid transitions | live | — |
| Terminal transitions | live | — |

### Domain metrics

| Панель | Статус | Недостающие метрики |
| --- | --- | --- |
| Authorizations | TODO | [`nomnom_payments_authorizations_total`](../../../../docs/observability/metric-inventory.md#domain) |
| Captures and refunds | TODO | [`nomnom_payments_captures_total`](../../../../docs/observability/metric-inventory.md#domain), [`nomnom_payments_refunds_total`](../../../../docs/observability/metric-inventory.md#domain) |
| Failed payment operations | TODO | [`nomnom_payments_authorizations_total`](../../../../docs/observability/metric-inventory.md#domain), [`nomnom_payments_captures_total`](../../../../docs/observability/metric-inventory.md#domain), [`nomnom_payments_refunds_total`](../../../../docs/observability/metric-inventory.md#domain) |

### Authorization

| Панель | Статус | Недостающие метрики |
| --- | --- | --- |
| Checks by result | TODO | [`nomnom_authz_checks_total`](../../../../docs/observability/metric-inventory.md#authz) |
| Denied by permission | TODO | [`nomnom_authz_checks_total`](../../../../docs/observability/metric-inventory.md#authz) |
| Errors and timeouts | TODO | [`nomnom_authz_checks_total`](../../../../docs/observability/metric-inventory.md#authz) |
| Check latency | TODO | [`nomnom_authz_check_duration_seconds`](../../../../docs/observability/metric-inventory.md#authz) |

Живых панелей: 26 из 46. TODO-панели нет в dashboard: она появляется после смены статуса метрик в инвентаре и регенерации.

## Alerts

Rule group `payments` в folder `nomnom` ([`alerts/rules.json`](alerts/rules.json)), push — `make grafana-cloud-push`. Правило создаётся, только когда его метрики «есть»: alert без данных ничего не защищает. Пороги — стартовые для тестового окружения; SLO burn rate — по целям [ADR 40](../../../../docs/adr/0040-service-slo-and-burn-rate-alerts.md).

| Alert | Severity | Статус | Недостающие метрики |
| --- | --- | --- | --- |
| Payments: gRPC technical errors | critical | активно | — |
| Payments: gRPC p95 latency | warning | активно | — |
| Payments: postgresql pool saturated | warning | ждёт метрик | [`db_client_connection_max`](../../../../docs/observability/metric-inventory.md#db) |
| Payments: postgresql pool checkouts queued | warning | ждёт метрик | [`db_client_connection_pending_requests`](../../../../docs/observability/metric-inventory.md#db) |
| Payments: postgresql pool wait p95 | warning | ждёт метрик | [`db_client_connection_wait_time_seconds`](../../../../docs/observability/metric-inventory.md#db) |
| Payments: postgresql pool checkout timeouts | warning | ждёт метрик | [`db_client_connection_timeouts_total`](../../../../docs/observability/metric-inventory.md#db) |
| Payments: Availability SLO burn rate 1h | critical | активно | — |
| Payments: Availability SLO burn rate 6h | warning | активно | — |
| Payments: Latency ≤ 0.25s SLO burn rate 1h | critical | активно | — |
| Payments: Latency ≤ 0.25s SLO burn rate 6h | warning | активно | — |

## Переходы

- **Logs** — Explore Loki по `service_name`, `deployment_environment_name`, `service_version` и интервалу dashboard. Переход log → trace — derived field `trace_id` data source Loki.
- **Traces** — Explore Tempo (TraceQL) по сервису и окружению: все, ошибки (`status=error`); у серий панелей ошибок и latency — data link «Traces этой серии»: spans с теми же attributes, что labels серии (`rpc.method`, `rpc.response.status_code`, `db.operation.name`, `nomnom.usecase.name`, `error.type`), у latency — не быстрее значения точки.
- **Profiles** — Explore Pyroscope (CPU) по service/environment/интервалу. Trace → profile (Span Profiles, только CPU) поддерживается runtime и включается в data source Tempo после проверки end-to-end.
- **Exemplars** — выключены: не подтверждено, что SDK histograms, Alloy и Grafana Cloud сохраняют exemplars (`inventory.ExemplarsEnabled`).
- **Deployments** — annotation по смене `service_version` в `target_info`; появляется, когда `target_info` получает статус «есть».
- **Серверные метрики зависимостей** — ссылки на community dashboards в шапке; панели не дублируются.

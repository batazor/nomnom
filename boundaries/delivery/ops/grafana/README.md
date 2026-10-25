<!-- Сгенерировано ops/grafana (make grafana-generate). Не редактировать вручную. -->

# Delivery / Overview

Overview-dashboard bounded context `delivery`: один на сервис, отвечает на вопросы incident — работает ли сервис, выросли ли ошибки и latency, какая зависимость деградировала, есть ли backlog, что с Inbox/Outbox и бизнес-потоком, где trace/log/profile.

| | |
| --- | --- |
| UID | `nomnom-delivery-overview` |
| Файл | [`dashboards/overview.json`](dashboards/overview.json) |
| Схема | `dashboard.grafana.app/v2` (Grafana 13) |
| Доставка | Git Sync, repository [`ops/grafana/git-sync/delivery.yaml`](../../../../ops/grafana/git-sync/delivery.yaml) |
| Генератор | [`ops/grafana`](../../../../ops/grafana/README.md), функция `overview.ServiceOverview` |
| service.name | `delivery` |

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
| `service` | список service.name из генератора (`delivery`) | `job=~"nomnom/(${service:pipe})"`. |
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
| Availability: SLI / SLO 99.5% | live | — |
| Availability: error budget remaining (30d) | live | — |
| Availability: burn rate | live | — |
| Latency ≤ 0.5s: SLI / SLO 95% | live | — |
| Latency ≤ 0.5s: error budget remaining (30d) | live | — |
| Latency ≤ 0.5s: burn rate | live | — |

### Runtime

| Панель | Статус | Недостающие метрики |
| --- | --- | --- |
| Threads | TODO | [`process_thread_count`](../../../../docs/observability/metric-inventory.md#runtime) |
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
| postgresql: operations | live | — |
| postgresql: p95 by operation | live | — |
| postgresql: pool connections | live | [`db_client_connection_idle_min`](../../../../docs/observability/metric-inventory.md#db), [`db_client_connection_max`](../../../../docs/observability/metric-inventory.md#db) |
| postgresql: pool saturation | TODO | [`db_client_connection_max`](../../../../docs/observability/metric-inventory.md#db) |
| postgresql: pending checkouts and timeouts | TODO | [`db_client_connection_pending_requests`](../../../../docs/observability/metric-inventory.md#db), [`db_client_connection_timeouts_total`](../../../../docs/observability/metric-inventory.md#db) |
| postgresql: pool wait / use / create p95 | TODO | [`db_client_connection_create_time_seconds`](../../../../docs/observability/metric-inventory.md#db), [`db_client_connection_use_time_seconds`](../../../../docs/observability/metric-inventory.md#db), [`db_client_connection_wait_time_seconds`](../../../../docs/observability/metric-inventory.md#db) |

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
| Transitions | TODO | [`nomnom_fsm_transitions_total`](../../../../docs/observability/metric-inventory.md#fsm) |
| Invalid transitions | TODO | [`nomnom_fsm_transitions_total`](../../../../docs/observability/metric-inventory.md#fsm) |
| Terminal transitions | TODO | [`nomnom_fsm_transitions_total`](../../../../docs/observability/metric-inventory.md#fsm) |

### Domain metrics

| Панель | Статус | Недостающие метрики |
| --- | --- | --- |
| Delivery flow | TODO | [`nomnom_delivery_assignments_total`](../../../../docs/observability/metric-inventory.md#domain), [`nomnom_delivery_completed_total`](../../../../docs/observability/metric-inventory.md#domain), [`nomnom_delivery_created_total`](../../../../docs/observability/metric-inventory.md#domain), [`nomnom_delivery_failed_total`](../../../../docs/observability/metric-inventory.md#domain), [`nomnom_delivery_pickups_total`](../../../../docs/observability/metric-inventory.md#domain), [`nomnom_delivery_reassignments_total`](../../../../docs/observability/metric-inventory.md#domain) |
| Delivery offers | TODO | [`nomnom_delivery_offers_total`](../../../../docs/observability/metric-inventory.md#domain) |
| Failures and reassignments by reason | TODO | [`nomnom_delivery_failed_total`](../../../../docs/observability/metric-inventory.md#domain), [`nomnom_delivery_reassignments_total`](../../../../docs/observability/metric-inventory.md#domain) |
| Failures without a catalog reason | TODO | [`nomnom_delivery_failed_total`](../../../../docs/observability/metric-inventory.md#domain) |
| Assignment latency | TODO | [`nomnom_delivery_assignment_latency_seconds`](../../../../docs/observability/metric-inventory.md#domain) |
| Delivery duration | TODO | [`nomnom_delivery_duration_seconds`](../../../../docs/observability/metric-inventory.md#domain) |

### Authorization

| Панель | Статус | Недостающие метрики |
| --- | --- | --- |
| Checks by result | TODO | [`nomnom_authz_checks_total`](../../../../docs/observability/metric-inventory.md#authz) |
| Denied by permission | TODO | [`nomnom_authz_checks_total`](../../../../docs/observability/metric-inventory.md#authz) |
| Errors and timeouts | TODO | [`nomnom_authz_checks_total`](../../../../docs/observability/metric-inventory.md#authz) |
| Check latency | TODO | [`nomnom_authz_check_duration_seconds`](../../../../docs/observability/metric-inventory.md#authz) |

Живых панелей: 25 из 45. TODO-панель показывает недостающие метрики и целевой PromQL; она становится живой после смены статуса в инвентаре и регенерации.

## Alerts

Rule group `delivery` в folder `nomnom` ([`alerts/rules.json`](alerts/rules.json)), push — `make grafana-cloud-push`. Правило создаётся, только когда его метрики «есть»: alert без данных ничего не защищает. Пороги — стартовые для тестового окружения; SLO burn rate — по целям [ADR 40](../../../../docs/adr/0040-service-slo-and-burn-rate-alerts.md).

| Alert | Severity | Статус | Недостающие метрики |
| --- | --- | --- | --- |
| Delivery: gRPC technical errors | critical | активно | — |
| Delivery: gRPC p95 latency | warning | активно | — |
| Delivery: postgresql pool saturated | warning | ждёт метрик | [`db_client_connection_max`](../../../../docs/observability/metric-inventory.md#db) |
| Delivery: postgresql pool checkouts queued | warning | ждёт метрик | [`db_client_connection_pending_requests`](../../../../docs/observability/metric-inventory.md#db) |
| Delivery: postgresql pool wait p95 | warning | ждёт метрик | [`db_client_connection_wait_time_seconds`](../../../../docs/observability/metric-inventory.md#db) |
| Delivery: postgresql pool checkout timeouts | warning | ждёт метрик | [`db_client_connection_timeouts_total`](../../../../docs/observability/metric-inventory.md#db) |
| Delivery: Availability SLO burn rate 1h | critical | активно | — |
| Delivery: Availability SLO burn rate 6h | warning | активно | — |
| Delivery: Latency ≤ 0.5s SLO burn rate 1h | critical | активно | — |
| Delivery: Latency ≤ 0.5s SLO burn rate 6h | warning | активно | — |
| Delivery: failures outside the reason catalog | warning | ждёт метрик | [`nomnom_delivery_failed_total`](../../../../docs/observability/metric-inventory.md#domain) |

## Переходы

- **Logs** — Explore Loki по `service_name`, `deployment_environment_name`, `service_version` и интервалу dashboard. Переход log → trace — derived field `trace_id` data source Loki.
- **Traces** — Explore Tempo (TraceQL) по сервису и окружению: все, ошибки (`status=error`); у серий панелей ошибок и latency — data link «Traces этой серии»: spans с теми же attributes, что labels серии (`rpc.method`, `rpc.response.status_code`, `db.operation.name`, `nomnom.usecase.name`, `error.type`), у latency — не быстрее значения точки.
- **Profiles** — Explore Pyroscope (CPU) по service/environment/интервалу. Span Profiles для этого runtime не поддерживаются: только корреляция service/version/time.
- **Exemplars** — выключены: не подтверждено, что SDK histograms, Alloy и Grafana Cloud сохраняют exemplars (`inventory.ExemplarsEnabled`).
- **Deployments** — annotation по смене `service_version` в `target_info`; появляется, когда `target_info` получает статус «есть».
- **Серверные метрики зависимостей** — ссылки на community dashboards в шапке; панели не дублируются.

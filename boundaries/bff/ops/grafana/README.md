<!-- Сгенерировано ops/grafana (make grafana-generate). Не редактировать вручную. -->

# BFF / Overview

Overview-dashboard bounded context `bff`: один на сервис, отвечает на вопросы incident — работает ли сервис, выросли ли ошибки и latency, какая зависимость деградировала, есть ли backlog, что с Inbox/Outbox и бизнес-потоком, где trace/log/profile.

| | |
| --- | --- |
| UID | `nomnom-bff-overview` |
| Файл | [`dashboards/overview.json`](dashboards/overview.json) |
| Схема | `dashboard.grafana.app/v2` (Grafana 13) |
| Доставка | Git Sync, repository [`ops/grafana/git-sync/bff.yaml`](../../../../ops/grafana/git-sync/bff.yaml) |
| Генератор | [`ops/grafana`](../../../../ops/grafana/README.md), функция `overview.ServiceOverview` |
| service.name | `bff` |

JSON — артефакт генератора: правки вносятся в `ops/grafana/overview/services.go` или в инвентарь, затем `make grafana-generate`. CI (`make grafana-check`) падает при расхождении.

## SLO

Цели за скользящие 30d, решение — [ADR 40](../../../../docs/adr/0040-service-slo-and-burn-rate-alerts.md). Business rejection бюджет не тратит.

| SLI | Цель | Error budget |
| --- | --- | --- |
| Availability | 99.5% | 0.5% |

## Data sources

Выбираются dashboard variables по типу plugin, без UID окружения: `metrics` (Prometheus API Grafana Cloud), `logs` (Loki), `traces` (Tempo), `profiles` (Pyroscope).

## Variables

| Variable | Источник | Назначение |
| --- | --- | --- |
| `environment` | `target_info` → `deployment_environment_name` | Фильтр окружения через join с `target_info`. |
| `service` | список service.name из генератора (`bff`) | `job=~"nomnom/(${service:pipe})"`. |
| `version` | `target_info` → `service_version` | Сравнение версий во время выкатки. |
| `service_instance` | `target_info` → `instance` (`service.instance.id`) | Диагностика экземпляра. |

`namespace` не добавлен: `k8s.namespace.name` не входит в принятые resource attributes. High-cardinality variables (`order_id`, `user_id`, `payment_id`, `event_id`, `workflow_id`) запрещены — для них traces и logs.

## Rows

### Service health

| Панель | Статус | Недостающие метрики |
| --- | --- | --- |
| Request rate by entry | TODO | [`router_http_requests_total`](../../../../docs/observability/metric-inventory.md#graphql), [`target_info`](../../../../docs/observability/metric-inventory.md#resource) |
| Errors by class | TODO | [`router_http_requests_error_total`](../../../../docs/observability/metric-inventory.md#graphql), [`router_http_requests_total`](../../../../docs/observability/metric-inventory.md#graphql), [`target_info`](../../../../docs/observability/metric-inventory.md#resource) |
| Technical error ratio | TODO | [`router_http_requests_error_total`](../../../../docs/observability/metric-inventory.md#graphql), [`router_http_requests_total`](../../../../docs/observability/metric-inventory.md#graphql), [`target_info`](../../../../docs/observability/metric-inventory.md#resource) |
| Latency p50 / p95 / p99 | TODO | [`router_http_request_duration_milliseconds`](../../../../docs/observability/metric-inventory.md#graphql), [`target_info`](../../../../docs/observability/metric-inventory.md#resource) |

### SLO

| Панель | Статус | Недостающие метрики |
| --- | --- | --- |
| Availability: SLI / SLO 99.5% | TODO | [`router_http_requests_error_total`](../../../../docs/observability/metric-inventory.md#graphql), [`router_http_requests_total`](../../../../docs/observability/metric-inventory.md#graphql), [`target_info`](../../../../docs/observability/metric-inventory.md#resource) |
| Availability: error budget remaining (30d) | TODO | [`router_http_requests_error_total`](../../../../docs/observability/metric-inventory.md#graphql), [`router_http_requests_total`](../../../../docs/observability/metric-inventory.md#graphql), [`target_info`](../../../../docs/observability/metric-inventory.md#resource) |
| Availability: burn rate | TODO | [`router_http_requests_error_total`](../../../../docs/observability/metric-inventory.md#graphql), [`router_http_requests_total`](../../../../docs/observability/metric-inventory.md#graphql), [`target_info`](../../../../docs/observability/metric-inventory.md#resource) |

### Pods

| Панель | Статус | Недостающие метрики |
| --- | --- | --- |
| Ready pods | TODO | [`kube_pod_status_ready`](../../../../docs/observability/metric-inventory.md#pods) |
| Restarts | TODO | [`kube_pod_container_status_restarts_total`](../../../../docs/observability/metric-inventory.md#pods) |
| Waiting containers by reason | TODO | [`kube_pod_container_status_waiting_reason`](../../../../docs/observability/metric-inventory.md#pods) |
| CPU by pod | TODO | [`container_cpu_usage_seconds_total`](../../../../docs/observability/metric-inventory.md#pods) |
| Memory working set by pod | TODO | [`container_memory_working_set_bytes`](../../../../docs/observability/metric-inventory.md#pods) |
| Network by pod | TODO | [`container_network_receive_bytes_total`](../../../../docs/observability/metric-inventory.md#pods), [`container_network_transmit_bytes_total`](../../../../docs/observability/metric-inventory.md#pods) |

### Dependencies

| Панель | Статус | Недостающие метрики |
| --- | --- | --- |
| Subgraph calls: rate | TODO | [`router_http_requests_total`](../../../../docs/observability/metric-inventory.md#graphql), [`target_info`](../../../../docs/observability/metric-inventory.md#resource) |
| Subgraph calls: errors | TODO | [`router_http_requests_error_total`](../../../../docs/observability/metric-inventory.md#graphql), [`target_info`](../../../../docs/observability/metric-inventory.md#resource) |
| Subgraph calls: p95 | TODO | [`router_http_request_duration_milliseconds`](../../../../docs/observability/metric-inventory.md#graphql), [`target_info`](../../../../docs/observability/metric-inventory.md#resource) |

### Certificates

| Панель | Статус | Недостающие метрики |
| --- | --- | --- |
| Time to expiry | live | — |
| Time to scheduled renewal | live | — |
| Ready | live | — |

Живых панелей: 3 из 19. TODO-панели нет в dashboard: она появляется после смены статуса метрик в инвентаре и регенерации.

## Alerts

Rule group `bff` в folder `nomnom` ([`alerts/rules.json`](alerts/rules.json)), push — `make grafana-cloud-push`. Правило создаётся, только когда его метрики «есть»: alert без данных ничего не защищает. Пороги — стартовые для тестового окружения; SLO burn rate — по целям [ADR 40](../../../../docs/adr/0040-service-slo-and-burn-rate-alerts.md).

| Alert | Severity | Статус | Недостающие метрики |
| --- | --- | --- | --- |
| BFF: Availability SLO burn rate 1h | critical | ждёт метрик | [`router_http_requests_error_total`](../../../../docs/observability/metric-inventory.md#graphql), [`router_http_requests_total`](../../../../docs/observability/metric-inventory.md#graphql) |
| BFF: Availability SLO burn rate 6h | warning | ждёт метрик | [`router_http_requests_error_total`](../../../../docs/observability/metric-inventory.md#graphql), [`router_http_requests_total`](../../../../docs/observability/metric-inventory.md#graphql) |

## Переходы

- **Logs** — Explore Loki по `service_name`, `deployment_environment_name`, `service_version` и интервалу dashboard. Переход log → trace — derived field `trace_id` data source Loki.
- **Traces** — Explore Tempo (TraceQL) по сервису и окружению: все, ошибки (`status=error`); у серий панелей ошибок и latency — data link «Traces этой серии»: spans с теми же attributes, что labels серии (`rpc.method`, `rpc.response.status_code`, `db.operation.name`, `nomnom.usecase.name`, `error.type`), у latency — не быстрее значения точки.
- **Profiles** — Explore Pyroscope (CPU) по service/environment/интервалу. Span Profiles для этого runtime не поддерживаются: только корреляция service/version/time.
- **Exemplars** — выключены: не подтверждено, что SDK histograms, Alloy и Grafana Cloud сохраняют exemplars (`inventory.ExemplarsEnabled`).
- **Deployments** — annotation по смене `service_version` в `target_info`; появляется, когда `target_info` получает статус «есть».

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

## Data sources

Выбираются dashboard variables по типу plugin, без UID окружения: `metrics` (Prometheus API Mimir), `logs` (Loki), `traces` (Tempo), `profiles` (Pyroscope).

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

### Dependencies

| Панель | Статус | Недостающие метрики |
| --- | --- | --- |
| Subgraph calls: rate | TODO | [`router_http_requests_total`](../../../../docs/observability/metric-inventory.md#graphql), [`target_info`](../../../../docs/observability/metric-inventory.md#resource) |
| Subgraph calls: errors | TODO | [`router_http_requests_error_total`](../../../../docs/observability/metric-inventory.md#graphql), [`target_info`](../../../../docs/observability/metric-inventory.md#resource) |
| Subgraph calls: p95 | TODO | [`router_http_request_duration_milliseconds`](../../../../docs/observability/metric-inventory.md#graphql), [`target_info`](../../../../docs/observability/metric-inventory.md#resource) |

Живых панелей: 0 из 7. TODO-панель показывает недостающие метрики и целевой PromQL; она становится живой после смены статуса в инвентаре и регенерации.

## Переходы

- **Logs** — Explore Loki по `service_name`, `deployment_environment_name`, `service_version` и интервалу dashboard. Переход log → trace — derived field `trace_id` data source Loki.
- **Traces** — Explore Tempo (TraceQL) по сервису и окружению: все, ошибки (`status=error`); панели ошибок и latency содержат data links на ошибочные и медленные traces.
- **Profiles** — Explore Pyroscope (CPU) по service/environment/интервалу. Span Profiles для этого runtime не поддерживаются: только корреляция service/version/time.
- **Exemplars** — выключены: не подтверждено, что SDK histograms, Alloy и Mimir сохраняют exemplars (`inventory.ExemplarsEnabled`).
- **Deployments** — annotation по смене `service_version` в `target_info`; появляется, когда `target_info` получает статус «есть».

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

## Data sources

Выбираются dashboard variables по типу plugin, без UID окружения: `metrics` (Prometheus API Mimir), `logs` (Loki), `traces` (Tempo), `profiles` (Pyroscope).

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
| Request rate by entry | TODO | [`nomnom_messaging_process_duration_seconds`](../../../../docs/observability/metric-inventory.md#messaging), [`rpc_server_duration_milliseconds`](../../../../docs/observability/metric-inventory.md#rpc), [`target_info`](../../../../docs/observability/metric-inventory.md#resource) |
| Errors by class | TODO | [`nomnom_messaging_process_duration_seconds`](../../../../docs/observability/metric-inventory.md#messaging), [`rpc_server_duration_milliseconds`](../../../../docs/observability/metric-inventory.md#rpc), [`target_info`](../../../../docs/observability/metric-inventory.md#resource) |
| Technical error ratio | TODO | [`nomnom_messaging_process_duration_seconds`](../../../../docs/observability/metric-inventory.md#messaging), [`rpc_server_duration_milliseconds`](../../../../docs/observability/metric-inventory.md#rpc), [`target_info`](../../../../docs/observability/metric-inventory.md#resource) |
| Latency p50 / p95 / p99 | TODO | [`rpc_server_duration_milliseconds`](../../../../docs/observability/metric-inventory.md#rpc), [`target_info`](../../../../docs/observability/metric-inventory.md#resource) |
| CPU | TODO | [`process_cpu_time_seconds_total`](../../../../docs/observability/metric-inventory.md#runtime), [`target_info`](../../../../docs/observability/metric-inventory.md#resource) |
| Memory | TODO | [`process_memory_usage_bytes`](../../../../docs/observability/metric-inventory.md#runtime), [`target_info`](../../../../docs/observability/metric-inventory.md#resource) |

### Runtime

| Панель | Статус | Недостающие метрики |
| --- | --- | --- |
| GC collections | TODO | [`cpython_gc_collections_total`](../../../../docs/observability/metric-inventory.md#runtime), [`target_info`](../../../../docs/observability/metric-inventory.md#resource) |
| Threads (gRPC worker pool) | TODO | [`process_thread_count`](../../../../docs/observability/metric-inventory.md#runtime), [`target_info`](../../../../docs/observability/metric-inventory.md#resource) |
| CPU by mode | TODO | [`process_cpu_time_seconds_total`](../../../../docs/observability/metric-inventory.md#runtime), [`target_info`](../../../../docs/observability/metric-inventory.md#resource) |

### Dependencies

| Панель | Статус | Недостающие метрики |
| --- | --- | --- |
| Payment provider: requests | TODO | [`nomnom_payments_provider_request_duration_seconds`](../../../../docs/observability/metric-inventory.md#provider), [`target_info`](../../../../docs/observability/metric-inventory.md#resource) |
| Payment provider: errors and timeouts | TODO | [`nomnom_payments_provider_request_duration_seconds`](../../../../docs/observability/metric-inventory.md#provider), [`target_info`](../../../../docs/observability/metric-inventory.md#resource) |
| Payment provider: p95 | TODO | [`nomnom_payments_provider_request_duration_seconds`](../../../../docs/observability/metric-inventory.md#provider), [`target_info`](../../../../docs/observability/metric-inventory.md#resource) |

### Kafka

| Панель | Статус | Недостающие метрики |
| --- | --- | --- |
| Consumed | TODO | [`messaging_client_consumed_messages_total`](../../../../docs/observability/metric-inventory.md#messaging-otel), [`target_info`](../../../../docs/observability/metric-inventory.md#resource) |
| Produced (Outbox Forwarder) | TODO | [`messaging_client_sent_messages_total`](../../../../docs/observability/metric-inventory.md#messaging-otel), [`target_info`](../../../../docs/observability/metric-inventory.md#resource) |
| Processing failures | TODO | [`nomnom_messaging_process_duration_seconds`](../../../../docs/observability/metric-inventory.md#messaging), [`target_info`](../../../../docs/observability/metric-inventory.md#resource) |
| Consumer lag | TODO | [`kafka_consumergroup_lag`](../../../../docs/observability/metric-inventory.md#messaging) |
| Retries and poison (DLQ) | TODO | [`nomnom_messaging_poison_messages_total`](../../../../docs/observability/metric-inventory.md#messaging), [`nomnom_messaging_retry_attempts_total`](../../../../docs/observability/metric-inventory.md#messaging), [`target_info`](../../../../docs/observability/metric-inventory.md#resource) |
| Processing duration p95 by event_type | TODO | [`nomnom_messaging_process_duration_seconds`](../../../../docs/observability/metric-inventory.md#messaging), [`target_info`](../../../../docs/observability/metric-inventory.md#resource) |
| Message age at processing p95 | TODO | [`nomnom_messaging_message_age_seconds`](../../../../docs/observability/metric-inventory.md#messaging), [`target_info`](../../../../docs/observability/metric-inventory.md#resource) |
| Inbox: processed / duplicates | TODO | [`nomnom_inbox_duplicates_total`](../../../../docs/observability/metric-inventory.md#messaging), [`nomnom_messaging_process_duration_seconds`](../../../../docs/observability/metric-inventory.md#messaging), [`target_info`](../../../../docs/observability/metric-inventory.md#resource) |
| Outbox pending | TODO | [`nomnom_outbox_pending_messages`](../../../../docs/observability/metric-inventory.md#outbox), [`target_info`](../../../../docs/observability/metric-inventory.md#resource) |
| Outbox oldest pending age | TODO | [`nomnom_outbox_oldest_pending_age_seconds`](../../../../docs/observability/metric-inventory.md#outbox), [`target_info`](../../../../docs/observability/metric-inventory.md#resource) |
| Outbox publish retries | TODO | [`nomnom_outbox_publish_retries_total`](../../../../docs/observability/metric-inventory.md#outbox), [`target_info`](../../../../docs/observability/metric-inventory.md#resource) |

### Transactions

| Панель | Статус | Недостающие метрики |
| --- | --- | --- |
| Transactions by outcome | TODO | [`nomnom_uow_transactions_total`](../../../../docs/observability/metric-inventory.md#uow), [`target_info`](../../../../docs/observability/metric-inventory.md#resource) |
| Transaction duration | TODO | [`nomnom_uow_duration_seconds`](../../../../docs/observability/metric-inventory.md#uow), [`target_info`](../../../../docs/observability/metric-inventory.md#resource) |
| Retries by conflict | TODO | [`nomnom_uow_retries_total`](../../../../docs/observability/metric-inventory.md#uow), [`target_info`](../../../../docs/observability/metric-inventory.md#resource) |

### Domain Lifecycle

| Панель | Статус | Недостающие метрики |
| --- | --- | --- |
| Transitions | TODO | [`nomnom_fsm_transitions_total`](../../../../docs/observability/metric-inventory.md#fsm), [`target_info`](../../../../docs/observability/metric-inventory.md#resource) |
| Invalid transitions | TODO | [`nomnom_fsm_transitions_total`](../../../../docs/observability/metric-inventory.md#fsm), [`target_info`](../../../../docs/observability/metric-inventory.md#resource) |
| Terminal transitions | TODO | [`nomnom_fsm_transitions_total`](../../../../docs/observability/metric-inventory.md#fsm), [`target_info`](../../../../docs/observability/metric-inventory.md#resource) |

### Domain metrics

| Панель | Статус | Недостающие метрики |
| --- | --- | --- |
| Authorizations | TODO | [`nomnom_payments_authorizations_total`](../../../../docs/observability/metric-inventory.md#domain), [`target_info`](../../../../docs/observability/metric-inventory.md#resource) |
| Captures and refunds | TODO | [`nomnom_payments_captures_total`](../../../../docs/observability/metric-inventory.md#domain), [`nomnom_payments_refunds_total`](../../../../docs/observability/metric-inventory.md#domain), [`target_info`](../../../../docs/observability/metric-inventory.md#resource) |
| Failed payment operations | TODO | [`nomnom_payments_authorizations_total`](../../../../docs/observability/metric-inventory.md#domain), [`nomnom_payments_captures_total`](../../../../docs/observability/metric-inventory.md#domain), [`nomnom_payments_refunds_total`](../../../../docs/observability/metric-inventory.md#domain), [`target_info`](../../../../docs/observability/metric-inventory.md#resource) |

### Authorization

| Панель | Статус | Недостающие метрики |
| --- | --- | --- |
| Checks by result | TODO | [`nomnom_authz_checks_total`](../../../../docs/observability/metric-inventory.md#authz), [`target_info`](../../../../docs/observability/metric-inventory.md#resource) |
| Denied by permission | TODO | [`nomnom_authz_checks_total`](../../../../docs/observability/metric-inventory.md#authz), [`target_info`](../../../../docs/observability/metric-inventory.md#resource) |
| Errors and timeouts | TODO | [`nomnom_authz_checks_total`](../../../../docs/observability/metric-inventory.md#authz), [`target_info`](../../../../docs/observability/metric-inventory.md#resource) |
| Check latency | TODO | [`nomnom_authz_check_duration_seconds`](../../../../docs/observability/metric-inventory.md#authz), [`target_info`](../../../../docs/observability/metric-inventory.md#resource) |

Живых панелей: 0 из 36. TODO-панель показывает недостающие метрики и целевой PromQL; она становится живой после смены статуса в инвентаре и регенерации.

## Переходы

- **Logs** — Explore Loki по `service_name`, `deployment_environment_name`, `service_version` и интервалу dashboard. Переход log → trace — derived field `trace_id` data source Loki.
- **Traces** — Explore Tempo (TraceQL) по сервису и окружению: все, ошибки (`status=error`); панели ошибок и latency содержат data links на ошибочные и медленные traces.
- **Profiles** — Explore Pyroscope (CPU) по service/environment/интервалу. Trace → profile (Span Profiles, только CPU) поддерживается runtime и включается в data source Tempo после проверки end-to-end.
- **Exemplars** — выключены: не подтверждено, что SDK histograms, Alloy и Mimir сохраняют exemplars (`inventory.ExemplarsEnabled`).
- **Deployments** — annotation по смене `service_version` в `target_info`; появляется, когда `target_info` получает статус «есть».
- **Серверные метрики зависимостей** — ссылки на community dashboards в шапке; панели не дублируются.

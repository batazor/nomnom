<!-- Сгенерировано ops/grafana (make grafana-generate). Не редактировать вручную. -->

# Platform / Overview

Серверная сторона общей инфраструктуры и pods всех прикладных неймспейсов. Клиентская сторона (Authorization, Dependencies) и Pods конкретного сервиса — в overview сервиса; оттуда ведёт ссылка сюда.

| | |
| --- | --- |
| UID | `nomnom-platform-overview` |
| Файл | [`platform.json`](platform.json) |
| Схема | `dashboard.grafana.app/v2` (Grafana 13) |
| Доставка | Git Sync, repository [`ops/grafana/git-sync/platform.yaml`](../git-sync/platform.yaml) |
| Генератор | [`ops/grafana`](../README.md), функция `overview.PlatformOverview` |
| Источники | Alloy Prometheus scrape: `infra/spicedb`, `infra/postgresql`, `infra/kubernetes` ([`alloy.yaml`](../../kind/manifests/observability/alloy.yaml)) |

## Variables

| Variable | Источник | Назначение |
| --- | --- | --- |
| `owner` | владельцы PostgreSQL (`orders`, `payments`, `delivery`) | label `owner` экспортера. |
| `namespace` | прикладные неймспейсы (`nomnom-orders`, `nomnom-payments`, `nomnom-delivery`, `nomnom-restaurants`, `nomnom-bff`) | Row Pods. |

Environment и version не фильтруются: у Prometheus-метрик инфраструктуры нет `target_info`.

## Rows

### SpiceDB

| Панель | Статус | Недостающие метрики |
| --- | --- | --- |
| Requests by method | live | — |
| Errors by method and code | live | — |
| Latency p95 by method | live | — |
| CheckPermission results | live | — |
| Dispatch cache hit ratio | live | — |
| Datastore latency (avg) by operation | live | — |
| CPU | live | — |
| Resident memory | live | — |

### PostgreSQL

| Панель | Статус | Недостающие метрики |
| --- | --- | --- |
| Up | live | — |
| Connections | live | — |
| Transactions | live | — |
| Buffer cache hit ratio | live | — |
| Deadlocks and conflicts | live | — |
| Temp files written | live | — |
| Database size | live | — |
| Replication slot lag | live | — |
| Replication slot active | live | — |

### CDC (Debezium)

| Панель | Статус | Недостающие метрики |
| --- | --- | --- |
| Connector tasks by status | live | — |
| Events published | live | — |
| Lag behind source | live | — |
| Queue fill | live | — |

### Valkey

| Панель | Статус | Недостающие метрики |
| --- | --- | --- |
| Up | TODO | [`redis_up`](../../../docs/observability/metric-inventory.md#valkey) |
| Memory used / max | TODO | [`redis_memory_max_bytes`](../../../docs/observability/metric-inventory.md#valkey), [`redis_memory_used_bytes`](../../../docs/observability/metric-inventory.md#valkey) |
| Evicted and expired keys | TODO | [`redis_evicted_keys_total`](../../../docs/observability/metric-inventory.md#valkey), [`redis_expired_keys_total`](../../../docs/observability/metric-inventory.md#valkey) |
| Keyspace hit ratio | TODO | [`redis_keyspace_hits_total`](../../../docs/observability/metric-inventory.md#valkey), [`redis_keyspace_misses_total`](../../../docs/observability/metric-inventory.md#valkey) |
| Commands | TODO | [`redis_commands_processed_total`](../../../docs/observability/metric-inventory.md#valkey) |
| Clients and keys | TODO | [`redis_connected_clients`](../../../docs/observability/metric-inventory.md#valkey), [`redis_db_keys`](../../../docs/observability/metric-inventory.md#valkey) |

### Pods

| Панель | Статус | Недостающие метрики |
| --- | --- | --- |
| Ready / total pods by namespace | live | — |
| Restarts by pod | live | — |
| Waiting containers by reason | live | — |
| CPU by namespace | live | — |
| Memory working set by namespace | live | — |
| Network by namespace | live | — |

### Certificates

| Панель | Статус | Недостающие метрики |
| --- | --- | --- |
| Time to expiry | live | — |
| Time to scheduled renewal | live | — |
| Ready | live | — |

Живых панелей: 30 из 36. TODO-панель показывает недостающие метрики и целевой PromQL; она становится живой после смены статуса в инвентаре и регенерации.

## Alerts

Rule group `platform` в folder `nomnom` ([`../alerts/platform.json`](../alerts/platform.json)), push — `make grafana-cloud-push`. Правило создаётся, только когда его метрики «есть»: alert без данных ничего не защищает. Пороги — стартовые для тестового окружения; SLO burn rate — по целям [ADR 40](../../../docs/adr/0040-service-slo-and-burn-rate-alerts.md).

| Alert | Severity | Статус | Недостающие метрики |
| --- | --- | --- | --- |
| SpiceDB: errors | critical | активно | — |
| SpiceDB: p95 latency | warning | активно | — |
| PostgreSQL: down | critical | активно | — |
| PostgreSQL: deadlocks | warning | активно | — |
| CDC: connector task failed | critical | активно | — |
| CDC: connector task not running | warning | активно | — |
| CDC: lag behind source | warning | активно | — |
| PostgreSQL: replication slot inactive | critical | активно | — |
| PostgreSQL: replication slot retains WAL | warning | активно | — |
| Pods: crash looping | critical | активно | — |
| Pods: restarting | warning | активно | — |
| Pods: not ready | warning | активно | — |
| Certificates: expiring | warning | активно | — |
| Certificates: not ready | critical | активно | — |


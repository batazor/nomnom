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

### Pods

| Панель | Статус | Недостающие метрики |
| --- | --- | --- |
| Ready / total pods by namespace | live | — |
| Restarts by pod | live | — |
| Waiting containers by reason | live | — |
| CPU by namespace | live | — |
| Memory working set by namespace | live | — |
| Network by namespace | live | — |

Живых панелей: 21 из 21. TODO-панель показывает недостающие метрики и целевой PromQL; она становится живой после смены статуса в инвентаре и регенерации.


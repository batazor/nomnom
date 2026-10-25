# Grafana dashboards as code

Генератор overview-dashboards сервисов на [Grafana Foundation SDK](https://github.com/grafana/grafana-foundation-sdk) (Go). Решение — [ADR 29](../../docs/adr/0029-grafana-dashboards-as-code.md).

```bash
make grafana-generate   # dashboards, README сервисов, docs/observability/metric-inventory.md
make grafana-check      # CI: go test, регенерация без диффа, gcx dev lint (закоммиченные + live-вариант)
make grafana-serve      # gcx dev serve: локальный preview (нужен gcx context с Grafana для запросов)
make grafana-tools      # go install gcx
```

| Каталог | Содержимое |
| --- | --- |
| `inventory/` | Инвентарь метрик: имя в Mimir, labels, источник, статус по service.name. Единственный источник для панелей и `docs/observability/metric-inventory.md`. |
| `overview/` | `ServiceOverview(options)` — общая структура rows; `services.go` — options и domain-панели каждого сервиса; `platform.go` — `PlatformOverview()` (SpiceDB, PostgreSQL, pods). |
| `dashboards/` | Артефакт `Platform / Overview` (`platform.json`) и его README. |
| `cmd/dashboards/` | CLI: запись артефактов, `-check`, `-preview DIR` (все метрики «есть»). |
| `git-sync/` | Git Sync `Repository` на сервис и на Platform (`provisioning.grafana.app/v1beta1`, read-only). |

## Версии

| Компонент | Версия |
| --- | --- |
| Grafana | 13.2.4 (schema `dashboard.grafana.app/v2`, Git Sync GA) |
| Foundation SDK | `github.com/grafana/grafana-foundation-sdk/go` v0.0.21, пакет `dashboardv2` |
| gcx | v1.7.1 (`GCX_VERSION` в Makefile) |
| PromQL parser | `github.com/prometheus/prometheus` v0.315.1 |

## Как панель становится живой

1. Сервис экспортирует метрику, Alloy доставляет её в Mimir (проверено запросом).
2. В `inventory/inventory.go` статус метрики для этого service.name меняется на `Present`.
3. `make grafana-generate` — TODO-панель заменяется запросами, README и инвентарь обновляются.

Генерация падает, если панель ссылается на метрику вне инвентаря, на незаявленный label или на high-cardinality label из `inventory.ForbiddenLabels`.

## Проверки

- `go test ./...` — стабильные uid, общий порядок rows, ни одной живой панели без метрик «есть», все панели живые при полном инвентаре, data sources только через variables, нет секретов и namespace окружения, отказ на неинвентаризованной метрике и `order_id`.
- `-check` — артефакты совпадают с генератором.
- `gcx dev lint run` — правила gcx, включая `target-valid-promql` для live-варианта.

Проверено вручную 2026-10-25 на `grafana/grafana:13.2.4` в Docker: все dashboards приняты API `dashboard.grafana.app/v2` и сохранены без потерь (section variable `worker` сохраняется); live-варианты отрендерены, 258 запросов выполнены через Grafana против Prometheus (тот же PromQL API, что у Mimir) без ошибок; Explore-ссылка Loki открывается с подставленными variables и интервалом; `Repository` прошёл `dryRun`. Grafana, Mimir и Alloy в kind пока не развёрнуты, поэтому end-to-end с реальными данными не выполнялся.

## Отступления

- Генератор в `ops/grafana`, а не в `tooling/grafana`: по конвенции репозитория системные ops-инструменты живут в `ops/`.
- `namespace` variable в overview сервисов не добавлена: `k8s.namespace.name` не входит в принятые resource attributes. Row Pods фильтрует по label `namespace` Prometheus-метрик Kubernetes (фиксированный неймспейс контекста), а variable `namespace` есть только в Platform / Overview.
- Runtime row BFF не строится: runtime-метрики Cosmo Router не инвентаризированы.

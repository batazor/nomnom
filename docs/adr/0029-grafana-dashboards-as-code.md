# 29. Grafana Dashboards as Code

Date: 2026-10-25

## Status

Accepted

Связан с [9. Использовать OpenTelemetry и Grafana observability stack](0009-использовать-opentelemetry-и-grafana-observability-stack.md), [12. Helm Charts per Deployable Application](0012-helm-charts-per-deployable-application.md), [17. Cloud Native Application Runtime](0017-cloud-native-application-runtime.md), [28. Selective Use of Watermill CQRS](0028-selective-use-of-watermill-cqrs.md)

## Context

ADR 9 принял OpenTelemetry, Alloy, Tempo, Mimir, Loki, Pyroscope и Grafana, но отложил dashboards. Нужен способ, при котором каждый deployable service получает одинаково устроенный overview для incident, панели не расходятся с реально экспортируемыми метриками, а dashboard проходит ревью вместе с кодом сервиса.

Ручной JSON в Grafana UI дрейфует: структура разных сервисов расходится, изменения не ревьюятся, datasource UID конкретного окружения попадают в файлы. Один системный dashboard на все сервисы смешивает владельцев и не масштабируется. Панели «на будущее» по неэкспортируемым метрикам выглядят как исправный сервис без данных.

На дату решения в репозитории нет развёрнутой Grafana, Alloy и Mimir; ни один backend-сервис не подключает OpenTelemetry SDK. Последний стабильный Grafana — 13.2.4.

## Decision

**Владение.** Каждый deployable service владеет одним overview-dashboard `<Service> / Overview` рядом со своими ops-файлами: `boundaries/<service>/ops/grafana/dashboards/overview.json` и `README.md`. Стабильный uid — `nomnom-<service>-overview`. Dashboards созданы для `orders` (роли `orders`, `orders-worker`, `orders-consumer`), `restaurants`, `delivery`, `payments` и BFF.

**Генерация.** Dashboards генерируются Grafana Foundation SDK (Go) в `ops/grafana`. Общая функция `overview.ServiceOverview(options)` задаёт состав и порядок rows (Service health, Runtime, Dependencies, Kafka, Use cases, Transactions, Domain Lifecycle, Domain metrics, Temporal, Kitchen, Authorization); options только включают rows по реальным зависимостям сервиса. Одинаковая структура гарантируется кодом и тестом, а не конвенцией. JSON — коммитимый артефакт; `make grafana-check` в CI регенерирует его и падает при расхождении.

**Инвентарь.** Панели строятся только на метриках инвентаря (`ops/grafana/inventory`, документ [`docs/observability/metric-inventory.md`](../observability/metric-inventory.md) генерируется из него) со статусом «есть» — экспортируется и доставляется в Mimir. Для остальных генератор рисует TODO-панель с недостающими метриками, ссылкой на инвентарь и целевым PromQL. Генерация падает на метрике вне инвентаря, незаявленном label или high-cardinality label (`order_id`, `event_id` и т. п.). Смена статуса в инвентаре превращает TODO в живую панель без правки dashboard-кода.

**Схема.** Используется dashboard schema v2 (`dashboard.grafana.app/v2`): Grafana ≥ 13, rows/tabs layout и section-level variables (row Temporal фильтрует worker отдельно от API). Для self-managed Grafana миграция v1 → v2 необратима: сохранённый v2 dashboard не конвертируется обратно в v1 без потерь, поэтому откат Grafana ниже 13 после перехода не поддерживается.

**Ссылки на data sources.** Только datasource variables по типу plugin (`metrics`, `logs`, `traces`, `profiles`); UID окружений, секреты и адреса в артефактах отсутствуют. Environment, version и instance выбираются через `target_info`, потому что resource attributes не promoted в labels (docs/observability.md).

**Доставка.** Git Sync (GA в Grafana 13): один read-only `Repository` (`provisioning.grafana.app/v1beta1`) на сервис указывает на `boundaries/<service>/ops/grafana/dashboards` в GitHub; репозиторий — source of truth, folder в Grafana соответствует владельцу. Dashboards не редактируются в UI (`editable: false`, `workflows: []`); единственный writer — генератор. Доступ к GitHub передаётся при создании `Repository` через secure-поля и не коммитится.

**gcx.** `gcx` (замена deprecated `grafanactl`) используется в CI для `gcx dev lint run` закоммиченных и live-вариантов (все метрики «есть»: проверка PromQL правилом `target-valid-promql`), локально — `gcx dev serve`, при необходимости — `gcx resources validate/push` против инстанса.

**Alert rules** создаются следующим шагом тем же путём: Foundation SDK, генерация из того же инвентаря, артефакты рядом с dashboard, проверка в CI. Сейчас не создаются.

## Consequences

Структура overview одинакова у всех сервисов и меняется в одном месте; dashboard ревьюится в том же PR, что и код сервиса. Панели не обещают сигналов, которых нет: на дату решения все панели — TODO, и dashboards честно показывают объём недостающей инструментации.

Нужны Go toolchain и gcx в CI; JSON-артефакты большие и меняются при каждом изменении генератора. Grafana ниже 13 не поддерживается. Git Sync требует доступа Grafana к GitHub и отдельного `Repository` на сервис. Ручные правки dashboard в UI невозможны: исследование ведётся копией или Explore.

Инструментация сервисов, развёртывание Alloy/Mimir/Grafana и data source provisioning (derived field `trace_id`, trace-to-profile) остаются отдельными изменениями; после них статусы инвентаря переводятся в «есть».

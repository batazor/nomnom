# 29. Grafana Dashboards as Code

Date: 2026-10-25

## Status

Accepted

Связан с [9. Использовать OpenTelemetry и Grafana observability stack](0009-использовать-opentelemetry-и-grafana-observability-stack.md), [12. Helm Charts per Deployable Application](0012-helm-charts-per-deployable-application.md), [17. Cloud Native Application Runtime](0017-cloud-native-application-runtime.md), [28. Selective Use of Watermill CQRS](0028-selective-use-of-watermill-cqrs.md)

## Context

ADR 9 принял OpenTelemetry, Alloy, Tempo, Grafana Cloud Metrics, Loki, Pyroscope и Grafana, но отложил dashboards. Нужен способ, при котором каждый deployable service получает одинаково устроенный overview для incident, панели не расходятся с реально экспортируемыми метриками, а dashboard проходит ревью вместе с кодом сервиса.

Ручной JSON в Grafana UI дрейфует: структура разных сервисов расходится, изменения не ревьюятся, datasource UID конкретного окружения попадают в файлы. Один системный dashboard на все сервисы смешивает владельцев и не масштабируется. Панели «на будущее» по неэкспортируемым метрикам выглядят как исправный сервис без данных.

На дату решения в репозитории нет развёрнутой Grafana и Alloy; ни один backend-сервис не подключает OpenTelemetry SDK. Последний стабильный Grafana — 13.2.4.

## Decision

**Владение.** Каждый deployable service владеет одним overview-dashboard `<Service> / Overview` рядом со своими ops-файлами: `boundaries/<service>/ops/grafana/dashboards/overview.json` и `README.md`. Стабильный uid — `nomnom-<service>-overview`. Dashboards созданы для `orders` (роли `orders`, `orders-worker`, `orders-consumer`), `restaurants`, `delivery`, `payments` и BFF.

**Генерация.** Dashboards генерируются Grafana Foundation SDK (Go) в `ops/grafana`. Общая функция `overview.ServiceOverview(options)` задаёт состав и порядок rows (Service health, SLO ([ADR 40](0040-service-slo-and-burn-rate-alerts.md)), Runtime, Dependencies, Kafka, Use cases, Transactions, Domain Lifecycle, Domain metrics, Temporal, Kitchen, Authorization); options только включают rows по реальным зависимостям сервиса. Rows разложены по вкладкам: Product (Flow, Domain metrics, Kitchen) — как идёт бизнес-процесс, Technical — как работает сервис; без продуктовых rows вкладок нет. Панели лежат в auto grid и скрываются правилом show/hide «Has data», если запрос ничего не вернул: панель ошибок без ошибок не занимает место. Одинаковая структура гарантируется кодом и тестом, а не конвенцией. JSON — коммитимый артефакт; `make grafana-check` регенерирует его и падает при расхождении (локально перед push; workflow `grafana-dashboards.yaml` — только ручной запуск, ADR 30).

**Инвентарь.** Панели строятся только на метриках инвентаря (`ops/grafana/inventory`, документ [`docs/observability/metric-inventory.md`](../observability/metric-inventory.md) генерируется из него) со статусом «есть» — экспортируется и доставляется в Grafana Cloud. Панели с остальными метриками в dashboard не выводятся: они перечислены как TODO в README dashboard вместе с недостающими метриками. Генерация падает на метрике вне инвентаря, незаявленном label или high-cardinality label (`order_id`, `event_id` и т. п.). Смена статуса в инвентаре превращает TODO в живую панель без правки dashboard-кода.

**Именование.** OTel-имена, instruments, units и attributes собственных метрик описаны в registry `ops/semconv` (формат semantic conventions, зависимость от semconv v1.44.0) и проверяются OpenTelemetry Weaver (`make semconv-check`): правила `naming_conventions`/`stability` из `opentelemetry-weaver-packages`, те же, что у репозитория semantic-conventions, плюс правила проекта `ops/semconv/policies/nomnom.rego` (namespace `nomnom.`, без type/unit в имени, `s` для длительностей, annotation unit у counters, без ID и сумм). Attributes из semconv переиспользуются (`error.type`, `messaging.destination.name`), собственные — с namespace `nomnom.`. Инвентарь хранит Prometheus-имена; тест генератора сверяет его с registry, поэтому имя в dashboard и проверенное Weaver имя не расходятся. Порядок для новой метрики: registry → инвентарь → код. Детали — [docs/observability.md](../observability.md#именование-метрик).

**Схема.** Используется dashboard schema v2 (`dashboard.grafana.app/v2`): Grafana ≥ 13, rows/tabs layout и section-level variables (row Temporal фильтрует worker отдельно от API). Для self-managed Grafana миграция v1 → v2 необратима: сохранённый v2 dashboard не конвертируется обратно в v1 без потерь, поэтому откат Grafana ниже 13 после перехода не поддерживается.

**Ссылки на data sources.** Только datasource variables по типу plugin (`metrics`, `logs`, `traces`, `profiles`); UID окружений, секреты и адреса в артефактах отсутствуют. Environment, version и instance выбираются через `target_info`, потому что resource attributes не promoted в labels (docs/observability.md).

**Доставка.** Git Sync (GA в Grafana 13): один read-only `Repository` (`provisioning.grafana.app/v1beta1`) на сервис указывает на `boundaries/<service>/ops/grafana/dashboards` в GitHub; репозиторий — source of truth, folder в Grafana соответствует владельцу. Dashboards не редактируются в UI (`editable: false`, `workflows: []`); единственный writer — генератор. Доступ к GitHub передаётся при создании `Repository` через secure-поля и не коммитится.

**gcx.** `gcx` (замена deprecated `grafanactl`) используется в `make grafana-check` для `gcx dev lint run` закоммиченных и live-вариантов (все метрики «есть»: проверка PromQL правилом `target-valid-promql`), локально — `gcx dev serve`, при необходимости — `gcx resources validate/push` против инстанса.

**Platform / Overview.** Серверная сторона общей инфраструктуры (SpiceDB, PostgreSQL) не входит в overview сервисов, но и не уходит в community dashboards: тот же генератор строит `Platform / Overview` (uid `nomnom-platform-overview`, `ops/grafana/dashboards/platform.json`, Git Sync `ops/grafana/git-sync/platform.yaml`) из тех же правил инвентаря. Источник — Prometheus scrape Alloy с `job="infra/<target>"`; у таких метрик нет `target_info`, поэтому environment/version не фильтруются. Pods сервиса (cAdvisor, kube-state-metrics) — row Pods в его overview с фильтром по неймспейсу контекста и префиксу pod; Platform показывает pods всех прикладных неймспейсов.

**Alert rules** генерируются тем же путём: Foundation SDK (`alerting`), правила рядом с rows (`overview.ServiceAlerts`, `overview.PlatformAlerts`), один rule group на dashboard в folder `nomnom`. Артефакты — формат provisioning API: `boundaries/<service>/ops/grafana/alerts/rules.json` и `ops/grafana/alerts/platform.json`, вне каталогов Git Sync. Правило создаётся, только когда все его метрики «есть» в инвентаре, — тот же критерий, что у живой панели: alert без данных ничего не защищает. Вместо UID datasource в артефакте placeholder `${metrics}`, push подставляет UID окружения. Каждое правило — instant-запрос значения (`A`) и порог server-side expression (`C`), окно rate/increase фиксировано (5m), no data = OK, labels `severity` (`critical` | `warning`) и `nomnom_dashboard`, annotation `dashboard_url`. Пороги — стартовые значения тестового окружения; burn rate alerts по SLO — [ADR 40](0040-service-slo-and-burn-rate-alerts.md); contact points и notification policy не генерируются и задаются в окружении. Доставка в тестовый stack — `make grafana-cloud-push`; `-check` и тесты генератора покрывают и правила.

## Consequences

Структура overview одинакова у всех сервисов и меняется в одном месте; dashboard ревьюится в том же PR, что и код сервиса. Панели не обещают сигналов, которых нет: на дату решения все панели — TODO, и dashboards честно показывают объём недостающей инструментации.

Нужны Go toolchain и gcx (`make grafana-tools`); JSON-артефакты большие и меняются при каждом изменении генератора. Grafana ниже 13 не поддерживается. Git Sync требует доступа Grafana к GitHub и отдельного `Repository` на сервис. Ручные правки dashboard в UI невозможны: исследование ведётся копией или Explore.

Инструментация сервисов, развёртывание Alloy и Grafana и data source provisioning (derived field `trace_id`, trace-to-profile) остаются отдельными изменениями; после них статусы инвентаря переводятся в «есть».

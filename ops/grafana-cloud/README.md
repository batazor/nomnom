# Grafana Cloud для тестов

Grafana Cloud (free tier) заменяет в тестах ещё не развёрнутые Mimir и Grafana ([ADR 9](../../docs/adr/0009-использовать-opentelemetry-и-grafana-observability-stack.md)): kind отправляет метрики приложений через Alloy, а dashboards ([ADR 29](../../docs/adr/0029-grafana-dashboards-as-code.md)) проверяются на реальных данных.

## Настройка

1. Создать stack на grafana.com.
2. Stack → **OpenTelemetry → Configure**: OTLP endpoint, Instance ID и token (`Generate now`, или access policy только с `metrics:write`).
3. Stack → **Administration → Service accounts**: аккаунт с ролью Editor и token — для `make grafana-cloud-push`.
4. `cp .env.grafana-cloud.example .env.grafana-cloud` и заполнить. Файл игнорируется git.

```bash
make kind-up
make kind-deploy GRAFANA_CLOUD=1   # Alloy в nomnom-observability, OTLP orders и orders-consumer → Alloy
make kind-grafana-cloud-stop       # остановить отправку, кластер остаётся
make kind-down
make grafana-cloud-push            # overview dashboards → folder nomnom в stack (gcx)
```

`push-dashboards.sh` собирает временный gcx config (token gcx кладёт в системный keychain) и загружает копии артефактов с annotation `grafana.app/folder: nomnom`: Editor в Grafana 13 создаёт dashboards в корне, но не может их читать и обновлять, а на созданный им folder получает права администратора. Закоммиченные артефакты не меняются.

Profile `grafana-cloud` ([skaffold.yaml](../kind/skaffold/skaffold.yaml)) запускает `before`-hook [`scripts/grafana-cloud.sh`](../kind/scripts/grafana-cloud.sh): Secret `grafana-cloud-otlp` из `.env.grafana-cloud` и [`manifests/observability/alloy.yaml`](../kind/manifests/observability/alloy.yaml). Без endpoint, instance ID или token hook падает. Releases `orders` и `orders-consumer` получают [`values/grafana-cloud.yaml`](../kind/values/grafana-cloud.yaml). Без `GRAFANA_CLOUD=1` kind не меняется, `kind-e2e` в облако ничего не отправляет.

## Лимиты free tier

Главный лимит — активные series метрик (10k) и data points per minute. Поэтому:

| Мера | Где |
| --- | --- |
| Только метрики: traces, logs и profiles в pipeline Alloy нет | `alloy.yaml` |
| Только `service.namespace=nomnom`, остальной OTLP отбрасывается | `otelcol.processor.filter "nomnom"` |
| Собственные метрики Alloy и Kubernetes не отправляются | `alloy.yaml` |
| Экспорт раз в 60 s | `OTEL_METRIC_EXPORT_INTERVAL` |
| Метрики только orders и orders-consumer (остальные сервисы OTel не подключили) | `values/grafana-cloud.yaml` |
| Отправка выключается без удаления кластера | `make kind-grafana-cloud-stop` |

Расход виден в stack (**Cost management → Usage**) или запросом `count by (job) ({job=~"nomnom/.+"})`: OTLP ingestion собирает `job` из `service.namespace/service.name`.

## Отличия от self-managed Mimir

Grafana Cloud промотирует `service_name` и `service_namespace` в labels каждой метрики; self-managed Mimir по [docs/observability.md](../../docs/observability.md) этого не делает. Dashboards на эти labels не опираются (environment/version выбираются через `target_info`), поэтому работают в обоих вариантах.

## Проверено

2026-10-25: Alloy v1.21.2 с этим config принял OTLP/HTTP, отправил точку `service.namespace=nomnom` в OTLP gateway `prod-eu-west-2` (`otelcol_exporter_sent_metric_points_total` = 1, без ошибок) и отфильтровал точку другого namespace.

2026-10-25, kind + `make kind-deploy GRAFANA_CLOUD=1`: Alloy в `nomnom-observability`, `orders` и `orders-consumer` экспортируют в stack `bestnomnom` 22 series (`go_*`, `process_cpu_time_seconds_total`, `target_info`; jobs `nomnom/orders`, `nomnom/orders-consumer`).

2026-10-25, `make grafana-cloud-push`: 5 overview dashboards в folder `nomnom`, повторный push обновляет их без ошибок. Все 34 запроса 19 живых панелей `Orders / Overview` выполнены против `grafanacloud-prom` без ошибок; данные есть у Runtime (GOMAXPROCS, CPU, Memory, Heap, Allocation rate, Goroutines). Панели gRPC, Kafka и Inbox пусты: в kind не было запросов и сообщений. `target_info` без `service_version` и `instance` (процессы не задают `service.version` и `service.instance.id`), поэтому переменные `version` и `service_instance` содержат только All.

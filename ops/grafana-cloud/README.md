# Grafana Cloud для тестов

Grafana Cloud (free tier) заменяет в тестах ещё не развёрнутые Mimir и Grafana ([ADR 9](../../docs/adr/0009-использовать-opentelemetry-и-grafana-observability-stack.md)): kind отправляет метрики приложений через Alloy, а dashboards ([ADR 29](../../docs/adr/0029-grafana-dashboards-as-code.md)) проверяются на реальных данных.

## Настройка

1. Создать stack на grafana.com.
2. Stack → **OpenTelemetry → Configure**: OTLP endpoint, Instance ID и token (`Generate now`, или access policy только с `metrics:write`).
3. `cp .env.grafana-cloud.example .env.grafana-cloud` и заполнить. Файл игнорируется git.

```bash
make kind-up
make kind-deploy GRAFANA_CLOUD=1   # Alloy в nomnom-observability, OTLP orders и orders-consumer → Alloy
make kind-grafana-cloud-stop       # остановить отправку, кластер остаётся
make kind-down
```

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

## Проверено

2026-10-25: Alloy v1.21.2 с этим config принял OTLP/HTTP, отправил точку `service.namespace=nomnom` в OTLP gateway `prod-eu-west-2` (`otelcol_exporter_sent_metric_points_total` = 1, без ошибок) и отфильтровал точку другого namespace.

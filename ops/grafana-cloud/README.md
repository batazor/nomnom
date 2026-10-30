# Grafana Cloud для тестов

Grafana Cloud (free tier) хранит метрики и заменяет в тестах ещё не развёрнутую Grafana ([ADR 9](../../docs/adr/0009-использовать-opentelemetry-и-grafana-observability-stack.md)): kind отправляет метрики приложений через Alloy, а dashboards ([ADR 29](../../docs/adr/0029-grafana-dashboards-as-code.md)) проверяются на реальных данных.

## Настройка

1. Создать stack на grafana.com.
2. Stack → **OpenTelemetry → Configure**: OTLP endpoint, Instance ID и token (`Generate now`, или access policy только с `metrics:write`).
3. Stack → **Administration → Service accounts**: аккаунт с ролью Editor и token — для `make grafana-cloud-push`.
4. `cp .env.grafana-cloud.example .env.grafana-cloud` и заполнить. Файл игнорируется git.

```bash
make kind-up
make kind-deploy GRAFANA_CLOUD=1   # Alloy в nomnom-observability; метрики и traces всех сервисов по OTLP, логи из stdout pods
make kind-grafana-cloud-stop       # остановить отправку, кластер остаётся
make kind-down
make grafana-cloud-push            # overview dashboards → folder nomnom в stack (gcx)
```

`push-dashboards.sh` собирает временный gcx config (token gcx кладёт в системный keychain) и загружает копии артефактов с annotation `grafana.app/folder: nomnom`: Editor в Grafana 13 создаёт dashboards в корне, но не может их читать и обновлять, а на созданный им folder получает права администратора. Закоммиченные артефакты не меняются.

Profile `grafana-cloud` ([skaffold.yaml](../kind/skaffold/skaffold.yaml)) добавляет к манифестам kustomization [`manifests/observability`](../kind/manifests/observability/kustomization.yaml): [`alloy.yaml`](../kind/manifests/observability/alloy.yaml) с конфигурацией [`alloy/*.alloy`](../kind/manifests/observability/alloy) (ConfigMap `alloy` из `configMapGenerator`), [`kube-state-metrics.yaml`](../kind/manifests/observability/kube-state-metrics.yaml) и Secret `grafana-cloud-otlp`, который `secretGenerator` собирает из `.env.grafana-cloud` (в кластер попадают только `GRAFANA_CLOUD_OTLP_*`; суффикс-хеш имени перезапускает Alloy при изменении файла). Без файла рендер Skaffold падает; с пустым endpoint, instance ID или token init container `require-grafana-cloud` не даёт Alloy стартовать, и status check завершается ошибкой ([ADR 34](../../docs/adr/0034-declarative-kind-bootstrap.md)). Releases `orders` и `orders-consumer` получают [`values/grafana-cloud.yaml`](../kind/values/grafana-cloud.yaml). Без `GRAFANA_CLOUD=1` kind не меняется, `kind-e2e` в облако ничего не отправляет.

## Лимиты free tier

Главный лимит — активные series метрик (10k) и data points per minute. Поэтому:

| Мера | Где |
| --- | --- |
| Метрики, traces и логи; profiles в pipeline Alloy нет | `alloy/*.alloy` |
| Traces — только `service.namespace=nomnom` (internal gateway задаёт его resource attribute в EnvoyProxy); логи — только stdout pods прикладных неймспейсов; `service.*`, `deployment.environment.name` и `k8s.*` — resource attributes (index labels и structured metadata по правилам OTLP ingestion Loki), `trace_id`, `span_id`, `severity` — поля записи | `alloy/apps.alloy`, `alloy/logs.alloy` |
| Только `service.namespace=nomnom`, остальной OTLP отбрасывается | `otelcol.processor.filter "nomnom"` |
| Собственные метрики Alloy не отправляются | `alloy/*.alloy` |
| Инфраструктура — только allowlist метрик (`prometheus.relabel`): SpiceDB без health-check и buckets datastore, PostgreSQL только база владельца, Envoy internal gateway только clusters GRPCRoute (~420 series), pods только прикладных неймспейсов и `condition="true"` readiness; kube-state-metrics генерирует три метрики pods | `alloy/infra.alloy`, `kube-state-metrics.yaml` |
| Экспорт раз в 60 s | `OTEL_METRIC_EXPORT_INTERVAL` |
| Values профиля получают все releases orders, restaurants, delivery и payments (BFF — Cosmo, своя telemetry) | `skaffold.yaml`, `values/grafana-cloud.yaml` |
| BFF (Cosmo Router): из метрик Router остаются `router.http.requests`, `.requests.error`, `.request.duration_milliseconds` с labels `wg.operation.type`, `wg.subgraph.name`, `http.status_code` | `values/bff-grafana-cloud.yaml`, `otelcol.processor.filter "nomnom"`, `otelcol.processor.transform "bff_metrics"` |
| Отправка выключается без удаления кластера | `make kind-grafana-cloud-stop` |

Расход виден в stack (**Cost management → Usage**) или запросом `count by (job) ({job=~"(nomnom|infra)/.+"})`: OTLP ingestion собирает `job` из `service.namespace/service.name`, у Prometheus scrape инфраструктуры `job` — `infra/spicedb`, `infra/postgresql`, `infra/internal-gateway`, `infra/kubernetes`.

## Traces и логи

Traces процессов и internal gateway приходят в тот же `otelcol.receiver.otlp` и уходят в Tempo stack (`grafanacloud-traces`) через OTLP gateway тем же токеном. Логи Alloy читает из stdout pods через API server (`loki.source.kubernetes`, ClusterRole `nomnom-alloy`: `pods/log`), разбирает JSON-запись ([docs/observability.md](../../docs/observability.md#логи)) и переводит её в OTLP: `service.*` и `deployment.environment.name` — resource attributes, `severity`, `trace_id`, `span_id` — поля записи (переход log → trace в Grafana). Строка не-JSON уходит как есть; сервис для неё — имя контейнера.

Internal gateway (EnvoyProxy `internal-gateway`, [`gateway.yaml`](../kind/manifests/gateway/gateway.yaml)) пишет span `ingress` — потомок client span вызывающего — и span `router <cluster> egress` на каждую попытку к backend'у, поэтому retries ([ADR 15](../../docs/adr/0015-использовать-internal-gateway-для-синхронных-вызовов-бизнес-api.md)) и время в gateway видны в trace. Без профиля gateway spans не пишет; kustomization [`manifests/observability`](../kind/manifests/observability/kustomization.yaml) включает `manifests/gateway` и добавляет ему tracing в Alloy.

Проверено 2026-10-25: тестовый span и запись лога приняты gateway (HTTP 200/204), span найден в Tempo с resource `service.namespace=nomnom`; логи pods всех пяти прикладных неймспейсов видны в `grafanacloud-logs`.

## Инфраструктура

Тот же Alloy собирает Prometheus-метрики для [Platform / Overview](../grafana/dashboards/README.md) и row Pods overview сервисов и отправляет их по OTLP мимо фильтра `service.namespace=nomnom`:

| Источник | Как | `job` |
| --- | --- | --- |
| SpiceDB | scrape `:9090` pod в `nomnom-authz` | `infra/spicedb` |
| PostgreSQL | `prometheus.exporter.postgres` на владельца (`orders`, `payments`, `delivery`), DSN из Secret `alloy-postgres` (chart [`ops/kind/bootstrap`](../kind/bootstrap) из тех же паролей, что базы) | `infra/postgresql` |
| Valkey | `prometheus.exporter.redis` на владельца (`restaurants`), ACL-пользователь `exporter` без доступа к ключам, пароль из Secret `alloy-valkey` (chart [`ops/kind/bootstrap`](../kind/bootstrap)) | `infra/valkey` |
| Internal gateway | scrape stats Envoy `:19001/stats/prometheus` pod `owning-gateway-name=internal` в `envoy-gateway-system` (NetworkPolicy `internal-gateway-ingress` пускает только Alloy): requests, класс ответа, retries, pending overflow, timeouts, connect failures, latency по clusters GRPCRoute. gRPC-ошибки приходят с HTTP 200 — статус вызова смотрится по метрикам сервисов | `infra/internal-gateway` |
| Kafka consumer lag | `prometheus.exporter.kafka` к брокеру `kafka.nomnom-infra`, только groups `orders` и `restaurants` (delivery и payments Kafka не читают) и только `kafka_consumergroup_lag` | `infra/kafka` |
| Pods | cAdvisor kubelet через API server proxy (ClusterRole `nomnom-alloy`: `nodes/proxy`) и kube-state-metrics v2.20.0 в `nomnom-observability` | `infra/kubernetes` |

Инфраструктурные неймспейсы без NetworkPolicy, поэтому отдельных разрешений Alloy не нужно.

## Promoted labels

Grafana Cloud промотирует `service_name` и `service_namespace` в labels каждой метрики. Dashboards на эти labels не опираются: environment и version выбираются через `target_info` ([docs/observability.md](../../docs/observability.md)).

## Проверено

2026-10-25: Alloy v1.21.2 с этим config принял OTLP/HTTP, отправил точку `service.namespace=nomnom` в OTLP gateway `prod-eu-west-2` (`otelcol_exporter_sent_metric_points_total` = 1, без ошибок) и отфильтровал точку другого namespace.

2026-10-25, kind + `make kind-deploy GRAFANA_CLOUD=1`: Alloy в `nomnom-observability`, `orders` и `orders-consumer` экспортируют в stack `bestnomnom` 22 series (`go_*`, `process_cpu_time_seconds_total`, `target_info`; jobs `nomnom/orders`, `nomnom/orders-consumer`).

2026-10-25, `make grafana-cloud-push`: 5 overview dashboards в folder `nomnom`, повторный push обновляет их без ошибок. Все 34 запроса 19 живых панелей `Orders / Overview` выполнены против `grafanacloud-prom` без ошибок; данные есть у Runtime (GOMAXPROCS, CPU, Memory, Heap, Allocation rate, Goroutines). Панели gRPC, Kafka и Inbox пусты: в kind не было запросов и сообщений. `target_info` без `service_version` и `instance` (процессы не задают `service.version` и `service.instance.id`), поэтому переменные `version` и `service_instance` содержат только All. Исправлено в chart orders: `service.instance.id` — имя pod (downward API), `service.version` — `image.tag`; атрибуты окружения задаются `telemetry.resourceAttributes`, а не `env.OTEL_RESOURCE_ATTRIBUTES`.

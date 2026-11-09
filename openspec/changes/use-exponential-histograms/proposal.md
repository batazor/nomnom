# Proposal

## Why

Stack Grafana Cloud free tier ограничен 10k активных series метрик, а kind с профилем `grafana-cloud` уже расходует 7,4k (billing `grafanacloud_instance_active_series`, 2026-11-09). Из 4,6k живых series сервисов около 3,9k — бакеты explicit histograms: `rpc_server_call_duration_seconds` даёт ~17,5 series на комбинацию labels (14 границ + `+Inf`, `_sum`, `_count`), так же устроены `rpc_client_*`, `db_client_operation_duration`, `nomnom_usecase_duration`, timers Temporal SDK и histograms OBI по Temporal server. Сэмплирование не помогает: число series задаёт кардинальность, а не объём трафика; логи (~2,7 ГБ/мес из 50) и traces далеко от квоты.

Base2 exponential histogram OpenTelemetry Grafana Cloud хранит как native histogram: одна series на комбинацию labels, а billing считает 0,25 series за каждый непустой бакет (`_sum` и `_count` бесплатны). При ограничении 40 бакетов histogram стоит не больше 10 billable series против ~17,5 сейчас, а при обычном разбросе длительностей — 1–5. Заодно исчезают ручные наборы границ в пяти языках (`SecondsBuckets` и его копии), а перцентили становятся точнее.

## What Changes

- **SDK сервисов.** `orders`, `controlplane` (Go), `delivery` (Rust), `restaurants` (TypeScript), `payments` (Python) агрегируют все histograms как base2 exponential с `max_size` 40 через view MeterProvider. Наборы explicit-границ (`SecondsBuckets`, `*_BUCKETS`, `buckets.py`, константы `mod.rs`) и advice `explicitBucketBoundaries` удаляются. Timers Temporal SDK в `orders` меняются вместе с MeterProvider.
- **OBI.** Метрики Temporal server — exponential (`default_histogram_aggregation` OTLP exporter в `obi/obi.yaml`).
- **Dashboards и alerts.** Генератор `ops/grafana` строит запросы к native histograms: `histogram_count`, `histogram_sum`, `histogram_quantile` без `_bucket`, latency SLI — `histogram_fraction`. Инвентарь различает native и классические histograms; суффиксы `_bucket`/`_count`/`_sum` у native histogram — ошибка генерации. Артефакты пересобираются.
- **Документация.** ADR 9 (стратегия histograms), ADR 40 (latency SLI, порог — степень двойки), `docs/observability.md`, инвентарь метрик, README `ops/grafana-cloud` (лимиты free tier).

Замер расхода series после перехода в изменение не входит.

Не входит:

- `bff`: Cosmo Router задаёт explicit-buckets views в коде (`router/pkg/metric/meter.go`), конфигурацией это не меняется; его histograms остаются классическими;
- метрики Prometheus scrape инфраструктуры (Envoy, SpiceDB, PostgreSQL, Valkey, Kafka, kubelet);
- отбрасывание неиспользуемых метрик в Alloy, сокращение OBI до серверной стороны и churn series после рестартов (`instance` = имя pod) — отдельные изменения.

## Capabilities

### New Capabilities

- `metric-histograms`: агрегация histograms в процессах и OBI, запросы dashboards и alerts к native histograms, latency SLI по `histogram_fraction`.

### Modified Capabilities

Нет.

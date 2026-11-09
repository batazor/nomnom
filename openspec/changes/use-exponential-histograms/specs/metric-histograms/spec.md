# Spec Delta

## ADDED Requirements

### Requirement: Процессы агрегируют histograms как base2 exponential
**ID:** FR-774

MeterProvider каждого процесса `orders`, `controlplane`, `delivery`, `restaurants` и `payments` SHALL агрегировать все histogram instruments OpenTelemetry, включая timers Temporal SDK, как base2 exponential histogram с `max_size` 40 и `max_scale` 20. Explicit-границы бакетов MUST NOT задаваться ни view, ни advice instrument. Histograms Watermill приходят через Prometheus bridge мимо views и остаются классическими.

#### Scenario: RPC histogram сервиса
**ID:** AC-1090

- **GIVEN** процесс сервиса запущен с включённым экспортом метрик
- **WHEN** обработан входящий gRPC-вызов и выполнен export
- **THEN** `rpc.server.call.duration` экспортирован как exponential histogram с не более чем 40 бакетами в положительном диапазоне
- **AND** в экспорте нет explicit-bucket histogram ни для одного instrument процесса

#### Scenario: Native histogram в stack
**ID:** AC-1091

- **GIVEN** kind развёрнут с `GRAFANA_CLOUD=1` и сервисы обработали вызовы
- **WHEN** в stack выполняется `histogram_count(rate(rpc_server_call_duration_seconds{job=~"nomnom/.+"}[5m]))`
- **THEN** есть результат для каждого gRPC-сервиса
- **AND** `count({job=~"nomnom/.+", job!="nomnom/bff", __name__=~".+_bucket"})` пуст

### Requirement: OBI экспортирует exponential histograms
**ID:** FR-775

OBI SHALL экспортировать histograms метрик Temporal server как base2 exponential histogram.

#### Scenario: RPC histogram Temporal server
**ID:** AC-1092

- **GIVEN** kind развёрнут с `GRAFANA_CLOUD=1` и Temporal server обрабатывает вызовы
- **WHEN** в stack выполняется `histogram_count(rate(rpc_server_call_duration_seconds{job="nomnom/temporal"}[5m]))`
- **THEN** есть результат
- **AND** series `rpc_server_call_duration_seconds_bucket{job="nomnom/temporal"}` не обновляются

### Requirement: Dashboards и alerts читают native histograms
**ID:** FR-776

Генератор `ops/grafana` SHALL строить запросы к histograms процессов и OBI через `histogram_count`, `histogram_sum`, `histogram_quantile` и `histogram_fraction` по базовому имени. Запрос к такой histogram с суффиксом `_bucket`, `_count` или `_sum` MUST завершать генерацию ошибкой. Классические histograms `bff` и Prometheus scrape SHALL запрашиваться через суффиксы, как раньше.

#### Scenario: Суффикс у native histogram
**ID:** AC-1093

- **GIVEN** панель генератора ссылается на `rpc_server_call_duration_seconds_count`
- **WHEN** выполняется `make grafana-generate`
- **THEN** генерация завершается ошибкой с именем метрики и панели

#### Scenario: Панели на данных
**ID:** AC-1094

- **GIVEN** kind развёрнут с `GRAFANA_CLOUD=1`, dashboards загружены `make grafana-cloud-push`, сервисы обработали вызовы
- **WHEN** выполняются запросы всех панелей overview-dashboards и Platform / Overview
- **THEN** ни один запрос не завершается ошибкой
- **AND** панели rate, error ratio и latency gRPC-сервисов возвращают данные

### Requirement: Latency SLI считается по native histogram
**ID:** FR-778

Latency SLI сервиса SHALL равняться `histogram_fraction(0, T, …)` по `rpc_server_call_duration_seconds` за окно SLO, где `T` — порог SLO. Порог MUST быть степенью двойки, чтобы совпадать с границей бакета при scale ≥ 0; иной порог MUST завершать генерацию ошибкой.

#### Scenario: Порог не степень двойки
**ID:** AC-1096

- **GIVEN** в `Options.SLO` сервиса задан порог latency 0.1 s
- **WHEN** выполняется `make grafana-generate`
- **THEN** генерация завершается ошибкой с именем сервиса и порогом

#### Scenario: SLI orders
**ID:** AC-1097

- **GIVEN** порог latency `orders` — 0.25 s
- **WHEN** генерируются row SLO и burn-rate alerts `orders`
- **THEN** запрос плохих событий latency — `1 - histogram_fraction(0, 0.25, sum(increase(rpc_server_call_duration_seconds{…}[окно]) and on (job, instance) …))`

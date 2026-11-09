# Tasks

## 1. Агрегация в процессах

- [ ] 1.1 `orders`, `controlplane`: view base2 exponential (`MaxSize` 40, `MaxScale` 20) вместо view с `SecondsBuckets`; удалить `SecondsBuckets`, домен-специфичные границы и `WithExplicitBucketBoundaries`; тесты экспорта (`FR-774`, `AC-1090`).
- [ ] 1.2 `delivery`: view `Aggregation::Base2ExponentialHistogram`; удалить константы границ `pkg/telemetry/mod.rs` и их использование; тесты `pkg/telemetry` (`FR-774`, `AC-1090`).
- [ ] 1.3 `restaurants`: view `EXPONENTIAL_HISTOGRAM` с `maxSize` 40; удалить `*_BUCKETS` и advice `explicitBucketBoundaries`; переписать проверки `buckets.boundaries` в `test/observability.test.ts` и соседних тестах (`FR-774`, `AC-1090`).
- [ ] 1.4 `payments`: view `ExponentialBucketHistogramAggregation`; удалить `pkg/telemetry/buckets.py`; тесты (`FR-774`, `AC-1090`).
- [ ] 1.5 OBI: `default_histogram_aggregation` в `obi/obi.yaml` или env DaemonSet — по факту в stack (`FR-775`).

## 2. Генератор dashboards и alerts

- [ ] 2.1 `ops/grafana/inventory`: тип `ClassicHistogram` для `bff` и Prometheus scrape; `BaseName` снимает суффиксы только у классических; ошибка на суффикс у native histogram (`FR-776`, `AC-1093`).
- [ ] 2.2 `ops/grafana/overview`: `Scope.Count`, `SumOf`, `Fraction`, `Quantile` без `_bucket`; перевести `rows.go`, `slo.go`, `service_alerts.go`, `platform.go`, `pool.go`; `SLO.Latency.Threshold` с проверкой степени двойки (`FR-776`, `FR-778`, `AC-1096`, `AC-1097`).
- [ ] 2.3 `make grafana-generate`: dashboards, alert rules, `docs/observability/metric-inventory.md`.

## 3. Документация

- [ ] 3.1 ADR 9 (стратегия histograms, billing native histograms), ADR 40 (latency SLI по `histogram_fraction`, порог — степень двойки), `docs/observability.md`, README сервисов, где описаны границы бакетов.

## 4. Проверка

- [ ] 4.1 Тесты `orders`, `controlplane`, `delivery`, `restaurants`, `payments`, `ops/grafana`; `make kind-deploy GRAFANA_CLOUD=1`; native histograms в stack (`AC-1091`, `AC-1092`); `make grafana-cloud-push` и запросы панелей (`AC-1094`); замер series в README `ops/grafana-cloud` (`FR-777`, `AC-1095`).

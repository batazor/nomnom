# Design

## Исходные данные

Замер 2026-11-09, stack `bestnomnom`, kind с `GRAFANA_CLOUD=1`:

| Что | Значение |
| --- | --- |
| `grafanacloud_instance_active_series` | 7450 (с churn после передеплоя) |
| Живые series `job=~"nomnom/.+"` | 4640 |
| Из них бакеты, `_sum`, `_count` histograms | ~3,9k |
| Крупнейшие | `rpc_server_call_duration_seconds_bucket` 882, `db_client_operation_duration_seconds_bucket` 750, `rpc_client_call_duration_seconds_bucket` 519, `nomnom_usecase_duration_seconds_bucket` 383 |
| `nomnom/temporal` (OBI) | 940, почти всё — rpc histograms |

## Billing native histograms

Grafana Cloud считает native histogram как `0,25 × непустые бакеты` активных series; `_sum` и `_count` не тарифицируются. OTLP exponential histogram со стандартной base2-схемой принимается как native histogram. Billing OTLP-series тот же, что у остальных.

Оценка: при ~3,9k series histograms сейчас ожидается 0,8–1,5k после перехода — итого 1,5–2,2k живых series сервисов вместо 4,6k. Оценка, не замер: число непустых бакетов зависит от разброса длительностей. Замер после перехода в это изменение не входит.

## Агрегация

Все histograms процесса — base2 exponential, параметры одинаковы во всех языках:

| Параметр | Значение | Почему |
| --- | --- | --- |
| `max_size` | 40 | Верхняя граница 10 billable series на histogram. При диапазоне 1 мс – 300 с (2^18) scale не опускается ниже 1 |
| `max_scale` | 20 | Значение SDK по умолчанию; scale уменьшается сам при росте разброса |
| `record_min_max` | `true` | Значение SDK по умолчанию |

Параметры — константы `pkg/telemetry` каждого сервиса, а не ключи конфигурации: dashboards и SLO рассчитаны на одну форму histogram, разные значения по окружениям ничего не дают.

Где задаётся:

| Сервис | Механизм |
| --- | --- |
| `orders` | функция-view `histogramView` на все histograms (`AggregationBase2ExponentialHistogram{MaxSize: 40, MaxScale: 20}`) вместо view Temporal с `SecondsBuckets` и view otelpgx |
| `controlplane` | `sdkmetric.NewView(sdkmetric.Instrument{Kind: sdkmetric.InstrumentKindHistogram}, sdkmetric.Stream{Aggregation: ExponentialHistogram})` |
| `delivery` | `with_view` с `Stream::builder().with_aggregation(Aggregation::Base2ExponentialHistogram { max_size: 40, max_scale: 20, record_min_max: true })` для `InstrumentKind::Histogram`; тип стабилен в `opentelemetry_sdk` 0.33 без `spec_unstable_metrics_views` |
| `restaurants` | view `instrumentType: HISTOGRAM`, `aggregation: { type: EXPONENTIAL_HISTOGRAM, options: { maxSize: 40 } }` |
| `payments` | `View(instrument_type=Histogram, aggregation=ExponentialBucketHistogramAggregation(max_size=40, max_scale=20))` |
| OBI | `meter_provider.readers[].periodic.exporter.otlp_grpc.default_histogram_aggregation: base2_exponential_bucket_histogram` (декларативный формат `file_format: '1.0'`); если OBI поле не применяет — `OTEL_EXPORTER_OTLP_METRICS_DEFAULT_HISTOGRAM_AGGREGATION` в DaemonSet. Выбор фиксируется при реализации по факту в stack |

View применяется ко всем histograms, включая advice instruments: при exponential-агрегации advice `explicitBucketBoundaries` игнорируется, поэтому advice и константы границ удаляются, а не остаются мёртвым кодом.

Temporal SDK (`contrib/opentelemetry`) пишет в MeterProvider процесса — его timers меняются тем же view. Watermill (`components/metrics`) пишет в `prometheus.Registerer`, а bridge отдаёт его histograms мимо views; builder Watermill не задаёт native-опции Prometheus, поэтому его две histograms (~24 series) остаются классическими (`ClassicHistogram` в инвентаре).

Go SDK создаёт stream на каждый совпавший view, поэтому в `orders` агрегация задаётся одной функцией-view на все histograms, которая заодно применяет allowlist attributes `db.client.operation.duration` otelpgx: отдельный view otelpgx дал бы второй stream. В `@opentelemetry/sdk-metrics` 3.1 у exponential-агрегации нет параметра `maxScale`, действует значение SDK 20.

## Запросы

Native histogram в Prometheus — одна series с базовым именем без суффиксов. Генератор переходит на функции PromQL для native histograms:

| Было | Стало |
| --- | --- |
| `rate(x_count[…])` | `histogram_count(rate(x[…]))` |
| `rate(x_sum[…])` | `histogram_sum(rate(x[…]))` |
| `histogram_quantile(q, sum by (le, …)(rate(x_bucket[…])))` | `histogram_quantile(q, sum by (…)(rate(x[…])))` |
| `increase(x_bucket{le="0.25"}[w]) / increase(x_count[w])` | `histogram_fraction(0, 0.25, sum by (…)(increase(x[w])))` |

Фильтр environment (`and on (job, instance) target_info…`) не меняется: он сопоставляет labels, а не тип сэмпла. Порядок важен: `histogram_count`/`histogram_fraction` применяются к уже отфильтрованному и просуммированному histogram, `sum` складывает native histograms поэлементно.

Инвентарь (`ops/grafana/inventory`) различает `Histogram` (OTel SDK, native) и `ClassicHistogram` (`bff`, Watermill, Prometheus scrape). `BaseName` снимает суффиксы только у классических; запрос с `_bucket`/`_count`/`_sum` для native histogram — ошибка генерации и теста, как сейчас неизвестная метрика. Scope получает `Count`, `SumOf`, `Fraction` рядом с `Rate`/`Over`/`Quantile`; строковые суффиксы в `rows.go`, `slo.go`, `service_alerts.go`, `platform.go`, `pool.go` уходят.

## Latency SLI

ADR 40 считает долю быстрых вызовов точно по границе бакета, без интерполяции. Границы base2 exponential — степени `2^(2^-scale)`; при scale ≥ 0 каждая степень двойки — граница бакета, поэтому `histogram_fraction(0, 0.25, …)` и `(0, 0.5, …)` точны. При `max_size` 40 и реальном диапазоне длительностей scale остаётся ≥ 1. Отсюда правило: порог latency SLO — степень двойки. `SLO.Latency.Bucket` переименовывается в `Threshold`, генератор отвергает порог, не являющийся степенью двойки.

## Переход

Сервисы и dashboards переключаются одним изменением. Смешение native и классической histogram под одним именем Grafana не рекомендует, а переходный режим с двумя наборами запросов противоречит отказу от фолбеков. Классические series перестают обновляться при первом деплое и выпадают из активных через ~20 минут; dashboards после деплоя видят только native. Окна SLO 30d в kind начинаются заново — для тестового окружения допустимо.

## Риски

- **Приём stack.** Если `bestnomnom` не принимает exponential histograms по OTLP, точки отбрасываются и в stack нет ни одной series histograms; проверяется первым же запросом `histogram_count(rate(rpc_server_call_duration_seconds[5m]))` (AC-1091).
- **Exemplars.** Выключены (`inventory.ExemplarsEnabled`) и этим изменением не включаются.
- **Grafana panels.** Heatmap и histogram panels Grafana 13 работают с native histograms; панели генератора, ожидающие `le`, проверяются на данных.

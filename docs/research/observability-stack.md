# Исследование observability-стека

Дата проверки источников: 2026-10-24.

Заметка фиксирует проверенные ограничения для будущих `docs/observability.md` и ADR. Это не конфигурация развёртывания и не решение о количестве экземпляров Grafana Alloy.

## Краткие выводы

- Предложенный путь сигналов поддерживается актуальными компонентами: приложения могут отправлять traces и metrics в Alloy по OTLP; Alloy экспортирует traces в Tempo по OTLP, application metrics в Grafana Cloud Metrics по OTLP/HTTP, scraped metrics туда же через Prometheus remote write, stdout logs в Loki и profiles через `pyroscope.receive_http` → `pyroscope.write` в Pyroscope.
- Единая идентичность должна начинаться с OTel Resource: `service.namespace=nomnom`, стабильные `service.name`, `service.version`, `deployment.environment.name` и, при необходимости, `service.instance.id`. Представление этих полей в хранилищах различается и требует явного маппинга.
- Для Kafka текущие OTel messaging conventions считают span links основным способом связи producer/consumer. Parent-child допустим для одиночного сообщения, но не является хорошим общим правилом для batch, fan-out, повторной доставки или обработки внутри уже активного контекста.
- Watermill `Message.Context()` не переносится через брокер сам по себе. Переносимый carrier — `Message.Metadata`; стандартный marshaler `watermill-kafka` отображает metadata один к одному в Kafka record headers и обратно. Поэтому W3C `traceparent`/`tracestate` надо inject/extract через metadata либо использовать встроенное Sarama OTel-обёртывание, предварительно проверив его семантику spans.
- Temporal следует инструментировать поддерживаемой SDK-интеграцией/interceptor/plugin, а не ручным span длиной во весь workflow. Workflow-код воспроизводится при replay, внешние операции должны выполняться в Activities, а telemetry из workflow должна быть replay-safe или подавляться при replay.
- Точная привязка profile к span неодинакова: официально Span Profiles поддерживаются для Go и Python из языков `nomnom`; Node.js и Rust в списке поддерживаемых языков отсутствуют. Для всех четырёх языков остаётся корреляция по сервису, версии и временному интервалу.

## OpenTelemetry: идентичность и контекст

### Resource attributes

OTel определяет `service.namespace`, `service.name`, `service.version` и `service.instance.id` как service resource attributes. `service.name` одинаков для всех горизонтальных экземпляров сервиса; `service.instance.id`, напротив, должен различать экземпляры. `service.version` — строка версии артефакта. `deployment.environment.name` — стабильный атрибут окружения, заменивший deprecated `deployment.environment` ([Service semantic conventions](https://opentelemetry.io/docs/specs/semconv/resource/service/), [Deployment attributes](https://opentelemetry.io/docs/specs/semconv/registry/attributes/deployment/)).

Рекомендуемый контракт для `nomnom`:

| OTel Resource | Правило |
| --- | --- |
| `service.namespace` | Всегда `nomnom` |
| `service.name` | Стабильное имя запускаемого приложения, а не bounded context вообще; отдельные API и worker могут иметь разные имена |
| `service.version` | Версия сборки или неизменяемый commit/release identifier |
| `deployment.environment.name` | `development`, `test`, `staging` или `production`, где это соответствует окружению |
| `service.instance.id` | Только когда нужен уникальный процесс/реплика; не использовать как обычное измерение бизнес-метрик |

OTel Resource должен прикрепляться ко всем сигналам в bootstrap/composition root. Доменные типы и `domains/<domain>/vo` об этих SDK-атрибутах знать не должны.

### Propagation

OTel по умолчанию использует W3C Trace Context; отправитель inject-ит context в carrier, получатель extract-ит его и строит локальный span. Для HTTP carrier — headers, для gRPC — metadata, для сообщений — metadata/headers сообщения ([OTel Context propagation](https://opentelemetry.io/docs/concepts/context-propagation/)). Нужно переносить как минимум `traceparent`; `tracestate` переносится вместе с ним, если присутствует. Baggage не следует использовать для токенов, платёжных данных или произвольных пользовательских payload.

Для логов OTel data model имеет отдельные необязательные `TraceId` и `SpanId`, а также timestamp, severity, body, resource и attributes. Это подтверждает формат JSON-логов с `trace_id`/`span_id` из активного context, но не требует отправлять логи по OTLP ([OTel Logs data model](https://opentelemetry.io/docs/specs/otel/logs/data-model/)).

### Semantic conventions и cardinality

Нужно предпочитать готовое instrumentation для HTTP, RPC/gRPC, database и messaging и сохранять принятые OTel semantic conventions. Конвенции находятся на разных уровнях stability, поэтому версии instrumentation и semantic conventions надо фиксировать при реализации, а не копировать нестабильный полный набор атрибутов в архитектурный контракт.

Duration-метрики должны быть histogram и измеряться в секундах; OTel называет duration дискретной операции `{operation}.duration` ([metric naming](https://opentelemetry.io/docs/specs/semconv/general/naming/), [Metrics semantic conventions](https://opentelemetry.io/docs/specs/semconv/general/metrics/)). Идентификаторы конкретного заказа, пользователя, workflow, trace или сообщения нельзя превращать в metric attributes: каждая уникальная комбинация становится отдельной time series. OTel SDK также определяет cardinality limits и Views с allow/exclude list атрибутов ([Metrics SDK](https://opentelemetry.io/docs/specs/otel/metrics/sdk/)).

Exemplars позволяют сохранить trace/span ID отдельного измерения внутри агрегированной метрики. По спецификации trace/span ID добавляются для synchronous instruments, когда measurement записан в активном context; trace-based exemplar filter выбирает измерения sampled spans. Поэтому переход metric → trace возможен только при поддержке SDK/exporter/backend и если соответствующий trace действительно сохранён ([Metrics SDK: Exemplar](https://opentelemetry.io/docs/specs/otel/metrics/sdk/#exemplar)).

## Alloy и хранилища

### Потоки

| Сигнал | Подтверждённый путь | Основание |
| --- | --- | --- |
| Traces | OTel SDK → OTLP → Alloy `otelcol.receiver.otlp` → `otelcol.exporter.otlp`/`otlphttp` → Tempo | Alloy принимает OTLP gRPC/HTTP и экспортирует OTLP; exporter имеет queue/retry для временных сбоев ([Tempo: Grafana Alloy](https://grafana.com/docs/tempo/latest/set-up-for-tracing/instrument-send/set-up-collector/grafana-alloy/)) |
| Application metrics | OTel SDK → OTLP → Alloy → OTLP/HTTP → Grafana Cloud Metrics | Grafana Cloud принимает OTLP/HTTP через OTLP gateway; OTLP рекомендован для OTel metrics ([Grafana Cloud OTLP](https://grafana.com/docs/grafana-cloud/send-data/otlp/)) |
| Infrastructure metrics | Prometheus-compatible `/metrics`/exporter → Alloy `prometheus.scrape` → `prometheus.remote_write` → Grafana Cloud Metrics | Alloy documented pipeline scrape → remote write; remote write имеет bounded in-memory queues и WAL ([Alloy remote write](https://grafana.com/docs/alloy/latest/reference/components/prometheus/prometheus.remote_write/)) |
| Logs | JSON stdout → platform-specific Alloy `loki.source.*` → `loki.process` → `loki.write` → Loki | Alloy имеет sources для file, Docker, journal, Kubernetes/pod logs и др.; конкретный source зависит от deployment ([Loki ingestion with Alloy](https://grafana.com/docs/loki/latest/send-data/alloy/)) |
| Profiles | Pyroscope SDK → Alloy `pyroscope.receive_http` → `pyroscope.write` → Pyroscope | Receiver совместим с Pyroscope ingest/push API и предназначен для SDK profiles ([Receive profiles from SDKs](https://grafana.com/docs/pyroscope/latest/configure-client/grafana-alloy/receive_profiles/), [`pyroscope.receive_http`](https://grafana.com/docs/alloy/latest/reference/components/pyroscope/pyroscope.receive_http/), [`pyroscope.write`](https://grafana.com/docs/alloy/latest/reference/components/pyroscope/pyroscope.write/)) |

Alloy — collector/processor/router, а не долговременное хранилище. Его queues, retries и WAL ограничены и при достаточно долгом outage допускают потерю; это согласуется с правилом, что отказ telemetry не должен останавливать бизнес-запрос. Нужно наблюдать exporter failures, dropped data, queue/WAL saturation и Alloy health, а размеры очередей и timeout задавать по окружению.

Один source должен иметь один основной путь. Например, не следует одновременно scrape-ить один `/metrics` несколькими Alloy без HA/deduplication и не следует одновременно отправлять один лог через stdout и OTLP Logs: документация Alloy отдельно предупреждает, что несколько scraper-ов одного target дают out-of-order samples, а несколько profile endpoints дублируют profiles ([Alloy remote write troubleshooting](https://grafana.com/docs/alloy/latest/reference/components/prometheus/prometheus.remote_write/), [`pyroscope.receive_http` example](https://grafana.com/docs/alloy/latest/reference/components/pyroscope/pyroscope.receive_http/)).

### Представление общей идентичности

| Хранилище | Представление |
| --- | --- |
| Tempo | OTel resource attributes сохраняются как resource attributes и запрашиваются через TraceQL, например `resource.service.namespace`, `resource.service.name`; это не требует переименования в приложении ([TraceQL query construction](https://grafana.com/docs/tempo/latest/traceql/construct-traceql-queries/)) |
| Метрики (Prometheus) | Metric attributes становятся Prometheus labels. `service.namespace` + `service.name` отображаются в `job`, `service.instance.id` — в `instance`; прочие resource attributes сохраняются в `target_info` и могут быть явно promoted to labels. Набор promoted labels фиксируется при реализации ([[Grafana resource mapping](https://grafana.com/docs/grafana-cloud/monitor-applications/application-observability/instrument/resource-attributes/)) |
| Loki | Для OTLP ingestion выбранные resource attributes становятся index labels с заменой `.` на `_`, остальные — structured metadata. Для stdout pipeline такой же небольшой label set надо сформировать в Alloy. Loki прямо запрещает high-cardinality request/user/trace IDs как labels ([Loki labels](https://grafana.com/docs/loki/latest/get-started/labels/)) |
| Pyroscope | SDK `application_name`/`appName` задаёт `service_name`; дополнительные стабильные измерения передаются tags/labels. В Pyroscope label names не допускают точку, поэтому OTel `service.namespace`, `service.version`, `deployment.environment.name` должны маппиться в согласованные `service_namespace`, `service_version`, `deployment_environment_name` ([Pyroscope client selection and tags](https://grafana.com/docs/pyroscope/latest/configure-client/), [Pyroscope API](https://grafana.com/docs/pyroscope/latest/reference-server-api/)) |

Для Loki stdout-path разумный минимальный индекс: `service_namespace`, `service_name`, `deployment_environment_name`; `service_version` может быть label только если число одновременно активных версий ограничено. `service_instance_id`, `trace_id`, `span_id`, `order_id`, `user_id`, `workflow_id` остаются JSON fields или structured metadata.

## Distributed tracing

### HTTP, gRPC и databases

- BFF создаёт SERVER span для входящего HTTP после extraction W3C context и CLIENT spans для исходящих gRPC calls.
- gRPC server создаёт SERVER span из gRPC metadata; client inject-ит W3C Trace Context в metadata.
- PostgreSQL и MongoDB наблюдаются поддерживаемым client instrumentation; SQL text, connection strings, credentials и document payload не должны записываться без редактирования.
- Имя span должно описывать класс операции, а не конкретный order/user/message ID: OTel отдельно требует обобщённые span names, иначе поиск и sampling получают неконтролируемую кардинальность ([Tracing API](https://opentelemetry.io/docs/specs/otel/trace/api/)).

### Kafka и Watermill

Актуальные messaging semantic conventions различают `create`, `send`, `receive`, `process`, `settle`. Producer должен прикреплять message creation context, consumer — извлекать его. Для `process`/`receive` span links являются основным механизмом связи: они работают для batch, нескольких consumers и обработки внутри другого ambient span. Только для одиночного сообщения creation context может быть parent consumer `process` span ([OTel messaging spans](https://opentelemetry.io/docs/specs/semconv/messaging/messaging-spans/)).

Для Watermill важны два разных context:

1. `Message.Context()` — process-local Go `context.Context`, используемый для cancellation/deadline и активного span внутри handler; он не сериализуется автоматически ([Watermill Message context](https://watermill.io/docs/message/#context)).
2. `Message.Metadata` — string map, который сохраняется Pub/Sub и аналогичен HTTP headers. `watermill-kafka` `DefaultMarshaler` копирует каждую metadata entry в Kafka record header, а `Unmarshal` восстанавливает metadata из headers ([watermill-kafka marshaler](https://github.com/ThreeDotsLabs/watermill-kafka/blob/master/pkg/kafka/marshaler.go)).

Следовательно, граница публикации должна inject-ить W3C `traceparent`/`tracestate` в `Message.Metadata`, а граница обработки — extract-ить их из metadata, положить полученный context в `Message.Context()` и создать consumer span. Новые исходящие messages получают новый creation context. Это можно реализовать собственным decorator/middleware поверх OTel propagator либо встроенным `watermill-kafka` `OTELEnabled`/Tracer, который оборачивает Sarama producer/consumer ([watermill-kafka publisher](https://github.com/ThreeDotsLabs/watermill-kafka/blob/master/pkg/kafka/publisher.go)). До выбора нужно проверить, что выбранная обёртка создаёт требуемые Watermill handler spans и следует текущим OTel messaging conventions, а не только Kafka client spans.

Повторная доставка — новая попытка обработки, но не новое бизнес-событие. Она получает отдельный processing span, связанный с исходным message creation context; retry/attempt можно отметить низкокардинальным атрибутом. Не следует держать один span открытым между publish, ожиданием в broker и всеми retries.

### Temporal

Temporal восстанавливает workflow, выполняя workflow code заново по Event History. Код должен принимать те же решения; network/database/file I/O выполняется в Activities, а Activity result при replay берётся из history ([Temporal Workflow replay](https://docs.temporal.io/workflows#how-workflow-replay-works)). Activity retry создаёт отдельную Activity Task execution, хотя бизнес-операция логически остаётся одной ([Temporal Tasks](https://docs.temporal.io/tasks#what-is-an-activity-task)).

Для Go официальный guide рекомендует OpenTelemetry v2 integration как replay-safe pre-release вариант. Поддерживаемый interceptor сериализует native propagation context в Temporal headers и переносит его через Workflow, Activity и Child Workflow boundaries; OTel использует W3C Trace Context ([Temporal Go observability](https://github.com/temporalio/documentation/blob/main/docs/develop/go/platform/observability.mdx#tracing)). Архитектурный документ должен обещать именно «поддерживаемую SDK-интеграцию», а конкретный package/version выбрать при реализации, учитывая pre-release статус v2 и несовместимость history между поколениями integration.

Правила:

- trace start/continuation проходит через Temporal Client/Worker integration, а не через ручное копирование обычного Go/Node/Python context в workflow state;
- spans внешних DB/API/Kafka операций создаются в Activities;
- workflow logs и metrics создаются через SDK-aware API, которое подавляет replay duplicates; при прямом доступе к raw metrics SDK требует явно учитывать replay state ([Temporal Go OTel metrics handler source](https://github.com/temporalio/sdk-go/blob/main/contrib/opentelemetry/handler.go));
- workflow span не должен жить всё календарное время long-running execution; Activities, child workflows, signals/updates и отдельные workflow tasks должны оставаться различимыми;
- `workflow_id`/`run_id` полезны как span/log fields, но не как metric labels.

## Profiles по языкам `nomnom`

Базовый обязательный тип — continuous CPU. Официальная матрица SDK подтверждает следующие дополнительные типы: Go — allocation/in-use, goroutine, mutex, block; Node.js — wall и heap, CPU требует `wall.collectCpuTime`; Rust — in-use objects/space через jemalloc; Python — allocation и in-use memory, причём memory выключена по умолчанию ([Profile types](https://grafana.com/docs/pyroscope/latest/configure-client/profile-types/)). Платформенная матрица отдельно показывает Linux/macOS/Windows ограничения; eBPF auto-instrumentation работает только на Linux ([Supported platforms](https://grafana.com/docs/pyroscope/latest/configure-client/supported-platforms/)).

| Приложение | Подключение | Существенные ограничения | Span profile |
| --- | --- | --- | --- |
| `orders` / Go | `github.com/grafana/pyroscope-go`, push в Alloy; альтернативно Go pull `/debug/pprof` или Linux eBPF | Push SDK умеет CPU, alloc/in-use, goroutine, mutex, block; pull требует открыть и защитить pprof endpoint | Да: отдельный `github.com/grafana/otel-profiling-go`; только CPU, 100 Hz означает, что spans короче ~10 ms могут не попасть в sample ([Go SDK source](https://github.com/grafana/pyroscope-go), [Go Span Profiles](https://grafana.com/docs/pyroscope/latest/configure-client/trace-span-profiles/go-span-profiles/)) |
| `restaurants` / Node.js | `@pyroscope/nodejs`, push в Alloy | Wall profiler; для CPU надо включить `wall.collectCpuTime`; heap поддерживается. Native package/platform build надо проверить на target image | Нет в официальной матрице Span Profiles; только корреляция service/version/time ([Node.js SDK](https://grafana.com/docs/pyroscope/latest/configure-client/language-sdks/nodejs/), [Span Profiles languages](https://grafana.com/docs/pyroscope/latest/configure-client/trace-span-profiles/)) |
| `delivery` / Rust | crate `pyroscope` + `backend-pprof-rs`, push в Alloy; memory требует jemalloc backend | Ограничения `pprof-rs`; более точный timer требует epoll на Linux или kqueue на macOS; корректный shutdown может занимать до 10 s; memory меняет global allocator на jemalloc | Нет в официальной матрице; только service/version/time ([Rust SDK](https://grafana.com/docs/pyroscope/latest/configure-client/language-sdks/rust/), [Span Profiles languages](https://grafana.com/docs/pyroscope/latest/configure-client/trace-span-profiles/)) |
| `payments` / Python | `pyroscope-io`, push в Alloy | CPU включён по умолчанию; memory opt-in и недоступна в free-threaded builds; profiler не fork-safe после запуска, поэтому Gunicorn/pre-fork worker инициализирует его после fork; macOS SIP влияет на system Python | Да: `pyroscope-otel`; только CPU, короткие spans могут не иметь samples ([Python SDK](https://grafana.com/docs/pyroscope/latest/configure-client/language-sdks/python/), [Python Span Profiles](https://grafana.com/docs/pyroscope/latest/configure-client/trace-span-profiles/python-span-profiles/)) |

Span Profiles требуют одновременно Pyroscope SDK, OTel tracing и отдельный language bridge. `pyroscope.profile.id` на span означает возможность связи, но не гарантирует наличие samples; I/O-bound или слишком короткий span может дать пустой профиль. eBPF profiles не дают span-profile correlation ([Grafana trace-to-profiles prerequisites](https://grafana.com/docs/grafana/latest/datasources/pyroscope/configure-traces-to-profiles/), [Pyroscope troubleshooting](https://grafana.com/docs/grafana-cloud/observe-and-act/connect-externally-hosted/data-sources/pyroscope/troubleshooting/)).

## Корреляция в Grafana

Grafana имеет встроенные data sources для Prometheus, Loki, Tempo и Pyroscope и настраиваемые links между signals ([Grafana data sources](https://grafana.com/docs/grafana/latest/datasources/)). Для `nomnom`:

- log → trace: Loki derived field извлекает `trace_id`; `trace_id` остаётся полем/structured metadata, не index label ([Trace-to-logs configuration](https://grafana.com/docs/grafana/latest/datasources/tempo/configure-tempo-data-source/configure-trace-to-logs/));
- trace → logs: Tempo data source формирует Loki query по time range и стабильным service/environment labels, при необходимости дополнительно фильтруя `trace_id`;
- metric → trace: exemplar хранит trace/span ID sampled context; ссылка не сработает, если trace был отброшен sampling;
- service/time → profile: запрос Pyroscope по `service_name`, `service_namespace`, `service_version`, environment и time range работает для всех языков;
- trace → profile: точная ссылка только при language bridge и `pyroscope.profile.id`; для `nomnom` это Go/Python, но не Node/Rust на дату исследования.

## Sampling, отказоустойчивость и безопасность

Head sampling принимает решение до знания результата всего trace и поэтому не может гарантировать сохранение всех traces с ошибкой. Для такой гарантии нужна tail sampling policy, но она требует stateful buffering, правильной маршрутизации всех spans trace в один sampler и дополнительных ресурсов ([OTel Sampling](https://opentelemetry.io/docs/concepts/sampling/), [Tempo sampling](https://grafana.com/docs/tempo/latest/set-up-for-tracing/instrument-send/set-up-collector/tail-sampling/)). Sampling и retention следует выбрать отдельно по окружениям позднее.

Даже tail sampling влияет на межсигнальные ссылки: log/exemplar может ссылаться на отброшенный trace. При генерации RED/service-graph metrics из spans генератор должен стоять до sampler, иначе метрики описывают только sample. Нельзя одновременно генерировать один и тот же набор span metrics в Alloy и Tempo без осознанной причины, иначе появятся дубли и лишняя стоимость ([Tempo: metrics from traces](https://grafana.com/docs/tempo/latest/metrics-from-traces/where-to-generate-metrics/)).

Telemetry должна быть best-effort относительно бизнес-операции:

- exporter startup/configuration error может сделать процесс unhealthy согласно политике приложения, но временный backend outage не должен ломать запрос или Activity;
- batch/queue/WAL имеют явные верхние границы; переполнение, dropped spans/logs/metrics/profiles и retry exhaustion наблюдаются собственными internal metrics/logs;
- tokens, credentials, payment details и полные customer payload удаляются до exporter; DB statements и error messages проходят allowlist/redaction;
- `order_id`, `user_id`, `workflow_id`, `trace_id`, `span_id`, message UUID и idempotency key допустимы как span/log fields при необходимости, но не как metric labels, Loki index labels или массовые profile tags.

## Открытые решения для этапа реализации

1. Топология Alloy: agent/sidecar/DaemonSet/gateway, количество слоёв и HA/deduplication.
2. Точный набор SDK/instrumentation packages и зафиксированные версии semantic conventions для Go, Node.js, Rust и Python.
3. Выбор между встроенным `watermill-kafka` OTel/Sarama tracing и собственным Watermill decorator/middleware, а также тест совместимости W3C headers с клиентами других языков.
4. Конкретная Temporal OTel integration/version и migration policy для long-running histories; v2 в Go документации отмечен как pre-release.
5. Promotion OTel resource attributes в labels метрик, Loki label set и единый reversible mapping dotted OTel names → underscore labels в Loki/Pyroscope.
6. Head/tail sampling, exemplar policy, retention, quotas, alert thresholds и SLO по окружениям.
7. Дополнительные profile types помимо CPU и допустимый overhead в каждом runtime; точная span-profile correlation остаётся только для поддерживаемых языков.
8. Конкретные stdout collectors для локального Docker и будущего Kubernetes; OTLP Logs рассматривается отдельно, чтобы не создать второй путь доставки.

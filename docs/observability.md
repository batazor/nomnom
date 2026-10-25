# Observability

Этот документ задаёт целевую архитектуру observability для BiteRoute: состав стека, движение телеметрии, общий контекст и правила инструментирования. Он не добавляет зависимости, конфигурацию Grafana Alloy, Docker Compose, Kubernetes-манифесты или единицы развёртывания. Решение зафиксировано в [ADR 9](adr/0009-использовать-opentelemetry-и-grafana-observability-stack.md).

## Ответственность компонентов

Observability разделяется на четыре независимые ответственности: приложение создаёт контекст и сигналы, Alloy принимает и обрабатывает телеметрию, специализированные хранилища сохраняют её, а Grafana предоставляет общий интерфейс анализа.

| Уровень | Компонент | Ответственность |
| --- | --- | --- |
| Инструментирование | [OpenTelemetry](https://opentelemetry.io/docs/) | API, SDK, semantic conventions, создание traces и metrics и распространение контекста. OpenTelemetry не является хранилищем. |
| Сбор | [Grafana Alloy](https://grafana.com/docs/alloy/latest/) | Приём OTLP, scrape Prometheus-compatible endpoints, чтение stdout, обработка, ограниченная нормализация и отправка всех сигналов. Alloy — единый логический инструмент сбора; количество экземпляров и топология развёртывания определяются отдельно. |
| Хранение | [Grafana Tempo](https://grafana.com/docs/tempo/latest/) | Хранение и поиск distributed traces. |
| Хранение | [Grafana Mimir](https://grafana.com/docs/mimir/latest/) | Долговременное хранение метрик и запросы через PromQL. |
| Хранение | [Grafana Loki](https://grafana.com/docs/loki/latest/) | Хранение и поиск структурированных логов с низкокардинальным индексом. |
| Хранение | [Grafana Pyroscope](https://grafana.com/docs/pyroscope/latest/) | Хранение и анализ continuous profiles. |
| Визуализация | [Grafana](https://grafana.com/docs/grafana/latest/datasources/) | Data sources, dashboards, Explore и переходы между сигналами. Grafana не становится путём доставки телеметрии. |

Отдельный Prometheus server не входит в схему: Alloy выполняет scrape, а Mimir хранит метрики и обслуживает PromQL. Profiles используют собственный протокол приёма Pyroscope, а не предполагаемый общий OTLP-путь.

## Потоки данных

| Сигнал | Основной путь |
| --- | --- |
| Traces | OpenTelemetry SDK → OTLP → Alloy → OTLP → Tempo |
| Метрики приложений | OpenTelemetry SDK → OTLP → Alloy → OTLP/HTTP → Mimir |
| Метрики инфраструктуры | Prometheus-compatible exporter или `/metrics` → Alloy scrape → remote write → Mimir |
| Logs | Структурированный JSON в stdout → Alloy → Loki |
| Profiles | Pyroscope SDK → Alloy `pyroscope.receive_http` → `pyroscope.write` → Pyroscope |

Для одного источника метрик или логов выбирается один основной путь доставки. Например, одна и та же метрика приложения не экспортируется одновременно через OTLP и `/metrics`, а один поток логов не отправляется одновременно напрямую и через stdout. Это предотвращает двойной подсчёт, дублирование записей и неочевидное владение retry-буферами.

Базовый путь логов — stdout. OTLP Logs можно оценить отдельно для конкретного runtime и SDK, если он даёт необходимую зрелость и эксплуатационное преимущество; его нельзя включать параллельно для тех же записей без явного разделения источников.

Grafana обращается к Tempo, Mimir, Loki и Pyroscope как к отдельным data sources. Межсигнальные переходы настраиваются в data sources и dashboards, а не посредством копирования всех данных в одно хранилище.

## Единая идентификация сервисов

Каждое запускаемое приложение задаёт следующие OpenTelemetry resource attributes:

| Resource attribute | Правило |
| --- | --- |
| `service.namespace` | Всегда `nomnom`. |
| `service.name` | Стабильное имя запускаемого приложения, не имя pod или hostname. API и worker одного bounded context могут иметь разные имена, например `orders-api` и `orders-worker`. |
| `service.version` | Неизменяемая версия сборки или commit/release identifier. |
| `deployment.environment.name` | Стабильное имя окружения, например `local`, `staging` или `production`. |
| `service.instance.id` | Уникальный идентификатор экземпляра, когда различение экземпляров действительно требуется. Он не заменяет `service.name`. |

Один и тот же набор значений используется для traces, metrics, logs и profiles. Для stdout-логов приложение записывает эквивалентные поля; Alloy нормализует их к принятой схеме. Конкретные имена процессов окончательно фиксируются вместе с топологией запуска.

### Представление в хранилищах

| Каноническое значение | Tempo | Mimir | Loki | Pyroscope |
| --- | --- | --- | --- | --- |
| `service.namespace` | resource attribute | вместе с `service.name` участвует в `job`; исходное значение доступно через `target_info`, отдельный `service_namespace` — только при явном promotion | индексируемый label `service_namespace` | label `service_namespace` |
| `service.name` | resource attribute и выбор сервиса | вместе с namespace участвует в `job`; исходное значение доступно через `target_info`, отдельный `service_name` — только при явном promotion | индексируемый label `service_name` | label `service_name` |
| `service.version` | resource attribute | `target_info`; `service_version` только при осознанном promotion | structured metadata/поле `service_version` | label `service_version` |
| `deployment.environment.name` | resource attribute | `target_info`; `deployment_environment_name` только при осознанном promotion | индексируемый label `deployment_environment_name` | label `deployment_environment_name` |
| `service.instance.id` | resource attribute | стандартное Prometheus-представление `instance`; не добавляется как измерение бизнес-метрик | structured metadata/поле `service_instance_id` | label только если это нужно для диагностики экземпляра |

При OTLP ingestion в Mimir metric attributes становятся Prometheus labels, а resource attributes по умолчанию в основном представлены через `job`, `instance` и `target_info`. Promotion дополнительных resource attributes в labels self-hosted Mimir является экспериментальной возможностью, поэтому архитектура не требует её включения. Если promotion будет выбран, точки преобразуются в подчёркивания и набор фиксируется явно и проверяется для закреплённой версии. Нельзя полагаться на разные неявные defaults SDK или backend.

`order_id`, `user_id`, `workflow_id`, `trace_id`, request ID, message ID и другие индивидуальные идентификаторы не становятся labels метрик или индексируемыми labels Loki. Они допустимы как поля логов, structured metadata Loki, span attributes или exemplars, когда это соответствует политике данных. Даже технический `service.instance.id` не добавляется ко всем метрикам автоматически: для быстро меняющихся реплик это создаёт ненужные series.

## Distributed tracing

Используются W3C [Trace Context](https://www.w3.org/TR/trace-context/) `traceparent` и `tracestate`. Baggage передаётся только по allowlist: входные значения являются недоверенными, влияют на объём и могут содержать чувствительные данные. Некорректный входящий trace context отклоняется, после чего создаётся новый trace.

### HTTP и gRPC

- BFF извлекает W3C Trace Context из входящего HTTP-запроса, создаёт server span и передаёт контекст во внутренние вызовы. В span сохраняются нормализованный route и безопасные HTTP-атрибуты, но не токены, query/body целиком или высококардинальный URL.
- gRPC client/server interceptors внедряют и извлекают контекст из metadata, создают spans вызова и фиксируют canonical status. Deadline и cancellation остаются механизмами gRPC и не подменяются tracing-контекстом.
- Автоинструментирование используется как основа, а ручные spans добавляются только для значимых операций, которые не видны из transport spans. Одну и ту же операцию нельзя одновременно оборачивать несколькими конкурирующими middleware.

### PostgreSQL и MongoDB

Инструментируются клиентские операции и пулы соединений. Spans содержат безопасные semantic attributes системы БД, операции и логического назначения; параметры запросов, credentials и полные документы не записываются. Захват текста statement отключается или санитизируется по allowlist. Бизнес-операция не получает span на каждую внутреннюю функцию репозитория: детализация должна помогать локализовать сетевую/серверную задержку, а не повторять call graph приложения.

### Kafka и Watermill

Producer внедряет `traceparent` и `tracestate` в Kafka headers, consumer извлекает их до создания span обработки. В `orders` OpenTelemetry propagator записывает W3C-поля в `message.Metadata`; Kafka marshaler Watermill переносит metadata в Kafka headers, а subscriber выполняет обратное отображение. Имена и кодировка служебных metadata фиксируются тестами совместимости, чтобы промежуточный код не потерял и не переименовал контекст.

Связь spans выбирается по семантике обработки; актуальные OpenTelemetry messaging conventions используют span links как общее правило связи creation context с receive/process:

- для одного сообщения и одной непосредственной обработки допустима parent-child связь consumer span с контекстом сообщения, но это частный случай, а не default проекта;
- при batch processing consumer span использует links на контексты всех сообщений, а не выбирает случайного родителя;
- при fan-in, отложенной обработке или когда работа имеет собственный жизненный цикл используются span links;
- повторная доставка создаёт отдельную попытку обработки. Она сохраняет связь с контекстом сообщения, но не изображается продолжением одного вручную открытого span; номер попытки фиксируется низкокардинальным атрибутом/полем, если он известен.

Операции publish, broker receive/delivery и process остаются различимыми. Один span не держится открытым на протяжении всего времени нахождения сообщения в Kafka.

### Temporal

Для `orders` используется поддерживаемая Go SDK интеграция OpenTelemetry через interceptors. На дату решения рекомендуемая Temporal OpenTelemetry v2 интеграция имеет pre-release статус, поэтому конкретный package/version и совместимость history выбираются и проверяются при реализации. Workflow-код остаётся детерминированным: он не обращается к обычным clocks, random, сетевым exporters или глобальным tracer/logger напрямую. Внешние операции и побочные эффекты инструментируются в Activities.

Replay не должен повторно создавать прикладные telemetry events. Workflow spans, logs и metrics создаются только через replay-aware механизмы Temporal SDK/interceptors; ручное инструментирование проверяет replay state и не считает повтор истории новой бизнес-операцией. Workflow execution/run identifiers сохраняются как span attributes и поля логов, но не как metric labels.

Trace context связывает запуск workflow, Activities и внешние вызовы настолько, насколько это поддерживает выбранная SDK-интеграция. Идентификаторы workflow обеспечивают поиск и диагностику, но не подменяют trace context. Нельзя моделировать весь жизненный цикл заказа или workflow одним вручную открытым span: start, task, Activity, signal/update и внешние операции должны оставаться понятными при долгом ожидании, retries и replay.

## Метрики

Базовый набор включает:

- частоту запросов, ошибки и длительность HTTP/gRPC;
- задержку публикации и обработки Kafka, число ошибок и consumer lag;
- обработанные и duplicate события, конфликты вставки Inbox, retries и возраст сообщения на момент обработки по [правилам событийного взаимодействия](event-driven-architecture.md#observability);
- длительность, ошибки и retries Temporal Activities;
- состояние Temporal workers, task queues и backlog/latency доступными server/SDK-метриками;
- CPU, память, GC/event loop/threads и другие runtime-specific показатели;
- использование, ожидание, ошибки и насыщение пулов PostgreSQL/MongoDB;
- бизнес-результаты: создание заказа, принятие рестораном, результат авторизации/списания/возврата, назначение и завершение доставки.

Для длительности используются гистограммы с границами, соответствующими сценарию. Они позволяют анализировать распределение и перцентили на стороне Mimir. Набор buckets, units и aggregation temporality фиксируется при реализации и проверяется нагрузочными данными.

Техническая попытка и бизнес-результат — разные события. Retry Activity, повторный RPC или повторная доставка Kafka-сообщения увеличивают счётчик попыток, но не счётчик созданных заказов. Бизнес-счётчик меняется в точке идемпотентно подтверждённого перехода состояния; имя результата и его допустимые низкокардинальные значения задаются явно.

## Логи

Приложение пишет по одной структурированной JSON-записи в stdout. Минимальная схема:

| Поле | Содержание |
| --- | --- |
| `timestamp` | Время события в UTC в однозначном машинном формате. |
| `severity` | Нормализованный уровень. |
| `message` | Краткое безопасное описание события. |
| `service.namespace`, `service.name`, `service.version` | Идентификация приложения. |
| `deployment.environment.name` | Окружение. |
| `trace_id`, `span_id` | Добавляются из активного tracing context; вне span отсутствуют, а не заполняются фиктивным значением. |
| Поля операции | Стабильное имя операции, результат и разрешённые идентификаторы поиска. |
| Поля ошибки | Тип/код ошибки и безопасное сообщение; stack trace добавляется отдельным структурированным полем по правилам runtime. |

Индекс Loki содержит только небольшой ограниченный набор вроде `service_namespace`, `service_name`, `deployment_environment_name` и `severity`. Версия, instance, trace ID, request ID, order ID и user ID остаются полями или structured metadata. Кардинальность и размер structured metadata контролируются отдельно.

До записи и экспорта данные редактируются по denylist и allowlist. Access/refresh tokens, cookies, credentials, платёжные реквизиты, секреты, полные пользовательские payload, DB bind parameters и содержимое Kafka-сообщений в telemetry не попадают. Ошибки внешних библиотек проходят через безопасное преобразование, поскольку их сообщения могут содержать запрос или payload.

## Continuous profiles

Базовый сигнал — continuous CPU profiling. Wall time, allocations/heap, mutex/lock и другие типы включаются только после проверки поддержки языка, runtime, платформы, накладных расходов и ценности для диагностики. Profiles идентифицируются теми же service, version и environment, что и остальные сигналы.

| Приложение | Подход | Существенные ограничения, проверяемые до включения |
| --- | --- | --- |
| `orders` — Go | `pyroscope-go`, push в Alloy; CPU profile как базовый, дополнительные Go profile types — по необходимости. | Частота, mutex/block rates и overhead; совместимость с `runtime/pprof`. Span Profiles доступны через отдельный OTel bridge только для CPU; короткий span может не получить sample. |
| `restaurants` — TypeScript/Node.js | `@pyroscope/nodejs`, push в Alloy; wall profiler с включённым сбором CPU time для базовой CPU-цели. | Native package/platform build и поддержка целевого Node runtime; heap подключается отдельно. Официальная матрица Span Profiles не включает Node.js, поэтому используется только service/version/time correlation. |
| `delivery` — Rust | crate `pyroscope` с `backend-pprof-rs`, push в Alloy; CPU profile после проверки на целевой платформе. | Таймер/unwinding, symbols, build flags, shutdown и overhead зависят от платформы; memory-профилирование требует jemalloc. Официальная матрица Span Profiles не включает Rust. |
| `payments` — Python | `pyroscope-io`, push в Alloy; CPU profile как базовый после проверки целевого CPython. | Native wheels, GIL, fork model и free-threaded runtime; Python 3.15 должен быть явно поддержан. Span Profiles доступны через отдельный bridge только для CPU; короткий span может не получить sample. |
| BFF | Подход соответствующего runtime после его выбора. | Решение о runtime предшествует выбору profiler SDK и способа span correlation. |

Факт совпадения `service.name`, `service.version` и временного интервала обеспечивает переход от сервиса к релевантному profile, но не доказывает принадлежность samples конкретному span. Для языков проекта официальная точная Span Profiles correlation документирована для Go и Python и только для CPU; Node.js и Rust используют корреляцию service/version/time. Даже атрибут `pyroscope.profile.id` означает возможность связи, а не наличие samples. Нельзя обещать одинаковые span profiles для всех языков.

## Корреляция в Grafana

Data sources и derived fields настраиваются так, чтобы поддерживать:

- переход из записи Loki в Tempo по полю `trace_id`;
- поиск связанных логов из trace по `trace_id`, service и временному интервалу;
- переход из метрики Mimir к trace через exemplars, если SDK, pipeline и тип метрики сохраняют exemplar;
- переход от service/version/environment и временного интервала к Pyroscope;
- переход от trace/span к profile только там, где SDK создаёт совместимую span correlation; в текущем наборе языков это Go и Python для CPU profiles.

`trace_id` в логе и exemplar являются ссылками, а не индексируемыми metric labels. Каждая связь проверяется end-to-end тестом после реализации pipeline: наличие атрибута в приложении ещё не гарантирует, что Alloy и backend его сохранили в ожидаемом виде.

## Размещение в коде

Observability дополняет, но не меняет принятую Vertical Slice Architecture:

- `domains/`, включая `domains/<domain>/vo/`, не импортируют OpenTelemetry, logger или Pyroscope SDK и не зависят от telemetry context;
- HTTP/gRPC middleware и interceptors, DB instrumentation, Kafka propagation и instrumentation внешних клиентов находятся в `infrastructure/` соответствующих slices или в переиспользуемой инфраструктуре того же запускаемого приложения;
- use cases наблюдаются через декораторы handlers и принятые application ports; доменные методы не получают tracer/logger только ради диагностики;
- providers, exporters, logger и profiler собираются существующим DI-механизмом в composition root каждого API или worker;
- resource attributes, SDK lifecycle, startup/shutdown, flush и ограниченные очереди настраиваются в bootstrap процесса; порядок инициализации и flush telemetry относительно readiness и graceful shutdown задан [cloud-native runtime contract](cloud-native-runtime.md#observability-lifecycle);
- BFF следует тем же правилам, но не получает слой `domains`.

Прикладной код может возвращать типизированный результат или ошибку, по которым внешний декоратор запишет metric/span/log. Это сохраняет наблюдаемость use case без зависимости бизнес-правил от конкретного SDK.

## Эксплуатационные правила

- Сбой, timeout или backpressure экспорта telemetry не останавливает бизнес-запрос и не меняет бизнес-результат. Экспорт выполняется асинхронно с ограниченными timeout и очередями.
- Буферы, retry-очереди и batch processors имеют явные ограничения. При исчерпании лимита допустима контролируемая потеря telemetry, но не неограниченный рост памяти или диска.
- Ошибки экспорта, dropped spans/logs/metrics/profiles, заполнение очередей и недоступность backend наблюдаются через внутренние метрики/логи SDK и Alloy. Для них позже задаются alerts.
- Sampling и retention задаются отдельно для каждого окружения и сигнала. Конкретные rates, tail policies, объёмы и сроки хранения этим документом не определяются.
- Обычный head sampling принимает решение до результата операции, поэтому не гарантирует сохранение всех ошибочных traces. Если такая гарантия понадобится, потребуется отдельное решение о tail sampling, его ресурсах и отказоустойчивости.
- Доступ к telemetry и её срок жизни учитывают чувствительность данных. Redaction в приложении предпочтительнее попытки удалить секрет после приёма.
- SLO, dashboards, alerts, recording rules и capacity planning определяются отдельными изменениями после появления рабочих сигналов и ожидаемой нагрузки.

## Отложенные решения

- количество экземпляров Alloy, agent/gateway topology и способ развёртывания;
- конкретные processors, batch sizes, retry/WAL settings, TLS и authentication между компонентами;
- SDK-библиотеки и закреплённые версии для каждого runtime;
- OTLP endpoint details, temporality и точное преобразование resource attributes;
- sampling, retention, multi-tenancy, storage sizing и disaster recovery;
- Loki labels/structured metadata limits и Mimir histogram strategy;
- exact dashboards, alerts, SLO и доступ к данным;
- BFF runtime и соответствующая profiling-интеграция.

## Официальные источники

- [OpenTelemetry concepts and signals](https://opentelemetry.io/docs/concepts/signals/), [resources](https://opentelemetry.io/docs/concepts/resources/) и [semantic conventions](https://opentelemetry.io/docs/specs/semconv/)
- [W3C Trace Context](https://www.w3.org/TR/trace-context/) и [OpenTelemetry context propagation](https://opentelemetry.io/docs/concepts/context-propagation/)
- [Grafana Alloy OpenTelemetry](https://grafana.com/docs/alloy/latest/collect/opentelemetry-data/), [Prometheus metrics](https://grafana.com/docs/alloy/latest/collect/prometheus-metrics/) и [Loki ingestion with Alloy](https://grafana.com/docs/loki/latest/send-data/alloy/)
- [Tempo](https://grafana.com/docs/tempo/latest/), [Mimir OTLP resource mapping](https://grafana.com/docs/mimir/latest/configure/configure-otel-collector/#work-with-default-opentelemetry-labels), [Loki labels](https://grafana.com/docs/loki/latest/get-started/labels/) и [Pyroscope SDK path through Alloy](https://grafana.com/docs/pyroscope/latest/configure-client/grafana-alloy/receive_profiles/)
- [Pyroscope profile types](https://grafana.com/docs/pyroscope/latest/configure-client/profile-types/), [supported platforms](https://grafana.com/docs/pyroscope/latest/configure-client/supported-platforms/) и [Span Profiles](https://grafana.com/docs/pyroscope/latest/configure-client/trace-span-profiles/)
- [Grafana trace correlations](https://grafana.com/docs/grafana/latest/datasources/tempo/configure-tempo-data-source/#trace-to-logs) и [exemplars](https://grafana.com/docs/grafana/latest/fundamentals/exemplars/)
- [Temporal Go observability](https://github.com/temporalio/documentation/blob/main/docs/develop/go/platform/observability.mdx#tracing), [Watermill message context and metadata](https://watermill.io/docs/message/) и [`watermill-kafka` metadata/header mapping](https://github.com/ThreeDotsLabs/watermill-kafka/blob/master/pkg/kafka/marshaler.go)

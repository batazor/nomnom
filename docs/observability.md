# Observability

Этот документ задаёт целевую архитектуру observability для nomnom: состав стека, движение телеметрии, общий контекст и правила инструментирования. Он не добавляет зависимости, конфигурацию Grafana Alloy, Docker Compose, Kubernetes-манифесты или единицы развёртывания; dashboards as code описаны в разделе [Dashboards](#dashboards). Решение зафиксировано в [ADR 9](adr/0009-использовать-opentelemetry-и-grafana-observability-stack.md).

## Ответственность компонентов

Observability разделяется на четыре независимые ответственности: приложение создаёт контекст и сигналы, Alloy принимает и обрабатывает телеметрию, специализированные хранилища сохраняют её, а Grafana предоставляет общий интерфейс анализа.

| Уровень | Компонент | Ответственность |
| --- | --- | --- |
| Инструментирование | [OpenTelemetry](https://opentelemetry.io/docs/) | API, SDK, semantic conventions, создание traces и metrics и распространение контекста. OpenTelemetry не является хранилищем. |
| Сбор | [Grafana Alloy](https://grafana.com/docs/alloy/latest/) | Приём OTLP, scrape Prometheus-compatible endpoints, чтение stdout, обработка, ограниченная нормализация и отправка всех сигналов. Alloy — единый логический инструмент сбора; количество экземпляров и топология развёртывания определяются отдельно. |
| Хранение | [Grafana Tempo](https://grafana.com/docs/tempo/latest/) | Хранение и поиск distributed traces. |
| Хранение | [Grafana Cloud Metrics](https://grafana.com/docs/grafana-cloud/send-data/otlp/) | Хранение метрик и запросы через PromQL. |
| Хранение | [Grafana Loki](https://grafana.com/docs/loki/latest/) | Хранение и поиск структурированных логов с низкокардинальным индексом. |
| Хранение | [Grafana Pyroscope](https://grafana.com/docs/pyroscope/latest/) | Хранение и анализ continuous profiles. |
| Визуализация | [Grafana](https://grafana.com/docs/grafana/latest/datasources/) | Data sources, dashboards, Explore и переходы между сигналами. Grafana не становится путём доставки телеметрии. |

Отдельный Prometheus server не входит в схему: Alloy выполняет scrape, а Grafana Cloud Metrics хранит метрики и обслуживает PromQL. Profiles используют собственный протокол приёма Pyroscope, а не предполагаемый общий OTLP-путь.

## Потоки данных

| Сигнал | Основной путь |
| --- | --- |
| Traces | OpenTelemetry SDK → OTLP → Alloy → OTLP → Tempo |
| Метрики приложений | OpenTelemetry SDK → OTLP → Alloy → OTLP/HTTP → Grafana Cloud Metrics |
| Метрики инфраструктуры | Prometheus-compatible exporter или `/metrics` → Alloy scrape → remote write → Grafana Cloud Metrics |
| Logs | Структурированный JSON в stdout → Alloy → Loki |
| Profiles | Pyroscope SDK → Alloy `pyroscope.receive_http` → `pyroscope.write` → Pyroscope |

Для одного источника метрик или логов выбирается один основной путь доставки. Например, одна и та же метрика приложения не экспортируется одновременно через OTLP и `/metrics`, а один поток логов не отправляется одновременно напрямую и через stdout. Это предотвращает двойной подсчёт, дублирование записей и неочевидное владение retry-буферами.

Базовый путь логов — stdout. OTLP Logs можно оценить отдельно для конкретного runtime и SDK, если он даёт необходимую зрелость и эксплуатационное преимущество; его нельзя включать параллельно для тех же записей без явного разделения источников.

Grafana обращается к Tempo, Grafana Cloud Metrics, Loki и Pyroscope как к отдельным data sources. Межсигнальные переходы настраиваются в data sources и dashboards, а не посредством копирования всех данных в одно хранилище.

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

| Каноническое значение | Tempo | Метрики (Prometheus) | Loki | Pyroscope |
| --- | --- | --- | --- | --- |
| `service.namespace` | resource attribute | вместе с `service.name` участвует в `job`; исходное значение доступно через `target_info`, отдельный `service_namespace` — только при явном promotion | индексируемый label `service_namespace` | label `service_namespace` |
| `service.name` | resource attribute и выбор сервиса | вместе с namespace участвует в `job`; исходное значение доступно через `target_info`, отдельный `service_name` — только при явном promotion | индексируемый label `service_name` | label `service_name` |
| `service.version` | resource attribute | `target_info`; `service_version` только при осознанном promotion | structured metadata/поле `service_version` | label `service_version` |
| `deployment.environment.name` | resource attribute | `target_info`; `deployment_environment_name` только при осознанном promotion | индексируемый label `deployment_environment_name` | label `deployment_environment_name` |
| `service.instance.id` | resource attribute | стандартное Prometheus-представление `instance`; не добавляется как измерение бизнес-метрик | structured metadata/поле `service_instance_id` | label только если это нужно для диагностики экземпляра |

При OTLP ingestion метрик metric attributes становятся Prometheus labels, а resource attributes по умолчанию в основном представлены через `job`, `instance` и `target_info`. Архитектура не требует promotion дополнительных resource attributes в labels. Если promotion будет выбран, точки преобразуются в подчёркивания и набор фиксируется явно и проверяется для закреплённой версии. Нельзя полагаться на разные неявные defaults SDK или backend.

### Именование метрик

Собственные метрики, их instruments, units и attributes описаны в registry [`ops/semconv`](../ops/semconv) (формат OpenTelemetry semantic conventions) и проверяются [OpenTelemetry Weaver](https://github.com/open-telemetry/weaver) командой `make semconv-check` (`make semconv-tools` ставит weaver; Homebrew `weaver` — другой инструмент). Проверка использует те же правила `naming_conventions` и `stability`, что и сам репозиторий semantic-conventions (пакет `opentelemetry-weaver-packages`, закреплённый commit), и правила проекта из [`ops/semconv/policies/nomnom.rego`](../ops/semconv/policies/nomnom.rego). Registry зависит от semantic conventions v1.44.0, поэтому общие attributes берутся оттуда, а не придумываются заново.

| Правило | Пример |
| --- | --- |
| OTel-имя в коде: строчные сегменты через точку, namespace `nomnom.<context>`; тип и unit в имя не входят (`total`, `seconds`, `ms`, `bytes` запрещены) | `nomnom.orders.placed`, не `nomnom_orders_placed_total` |
| Unit — в instrument: длительность и возраст — `s`; counter — annotation в фигурных скобках | `nomnom.orders.fulfillment.duration` (`s`), `nomnom.orders.placed` (`{order}`) |
| Attribute существует в semantic conventions — используется он | `error.type`, `messaging.destination.name` |
| Собственный attribute — с namespace `nomnom.`, чтобы не столкнуться с будущими semconv | `nomnom.outcome`, `nomnom.order.state`, `nomnom.usecase.name` |
| Идентификаторы и суммы не становятся attributes | `order_id`, `amount` — в traces и logs |

Имя в Prometheus выводится из OTel-имени: точки → `_`, unit `s` → `_seconds`, annotation unit не добавляется, counter → `_total` (`nomnom.messaging.process.duration` → `nomnom_messaging_process_duration_seconds`, attribute `nomnom.usecase.name` → label `nomnom_usecase_name`). Grafana Cloud добавляет эти суффиксы при OTLP ingestion. Инвентарь dashboards (`ops/grafana/inventory`) хранит эти Prometheus-имена, а тест `TestInventoryMatchesSemconvRegistry` сверяет его с registry: метрика, тип и набор labels совпадают в обе стороны. Новая метрика добавляется сначала в registry, затем в инвентарь и код. Histograms с unit `s` задают явные границы buckets в секундах: дефолтные границы OTel SDK рассчитаны на миллисекунды.

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

Через Outbox trace продолжается во всех контекстах одинаково: при записи события в Outbox сохраняется `traceparent` (и `tracestate`, где он есть) текущего span — обычно span use case. Relay или Forwarder публикует запись в PRODUCER span `send <topic>`, дочернем к сохранённому контексту, и header `traceparent` несёт этот producer span. Consumer создаёт CONSUMER span `process <topic>` дочерним к header и уже в нём — span use case и обращений к хранилищу. Некорректный `traceparent` начинает новый trace. Реализации: `orders` — `messaging.InjectTraceContext`/`ExtractTraceContext` (`pkg/messaging/propagation.go`) и otelsarama; `restaurants` — `KafkaPublisher` и consumer (`pkg/infrastructure/kafka`); `payments` — relay (`infrastructure/messaging`), consumer нет; `delivery` — relay (`infrastructure/outbox`), consumer нет.

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

Для длительности используются гистограммы с границами, соответствующими сценарию. Они позволяют анализировать распределение и перцентили на стороне backend метрик. Набор buckets, units и aggregation temporality фиксируется при реализации и проверяется нагрузочными данными.

Техническая попытка и бизнес-результат — разные события. Retry Activity, повторный RPC или повторная доставка Kafka-сообщения увеличивают счётчик попыток, но не счётчик созданных заказов. Бизнес-счётчик меняется в точке идемпотентно подтверждённого перехода состояния; имя результата и его допустимые низкокардинальные значения задаются явно.

### Watermill (`orders`)

Встроенные метрики Watermill (`components/metrics`, `PrometheusMetricsBuilder`) — базовый слой Kafka-метрик Go-процессов: `subscriber_messages_received_total{acked,handler_name,subscriber_name}`, `handler_execution_time_seconds{handler_name,success}`, `publish_time_seconds{success,handler_name,publisher_name}`. Namespace — `watermill`, subsystem — deployable role (`consumer`, `outbox_forwarder`), поэтому имена различаются между процессами и не коллидируют: например, `watermill_consumer_handler_execution_time_seconds`.

Экспорт идёт через OTel Prometheus bridge в общий OTLP pipeline с общими resource attributes. Отдельный `/metrics` scrape допустим только если bridge неприменим, и тогда — на техническом порту ([cloud-native runtime](cloud-native-runtime.md)); два пути для одних метрик не используются.

Встроенных метрик недостаточно. Собственный OTel middleware в composition root добавляет retry attempts по классу ошибки, poison/DLQ count, Inbox duplicates, processing duration по `nomnom.event.type` и message age (`now − occurred_at`). Attributes — только `nomnom.messaging.handler`, `nomnom.event.type`, `messaging.destination.name`, `nomnom.error.class`; `event_id` и `aggregate_id` не используются. Consumer lag берётся из Kafka-клиента или Alloy Kafka exporter, а не из Watermill. Outbox pending и oldest pending age — SQL-метрики Forwarder-роли, а не publish-декоратора.

Poison messages направляются Watermill `PoisonQueue` middleware в DLQ-топик `<stream>.dlq` (Requeuing After Error). Целевой порядок middleware: OTel/metrics → Retry (transient) → PoisonQueue (permanent) → handler; business errors не ретраятся. В `orders-consumer` цепочка реализована в `pkg/messaging/consumer.go` (Recoverer → OTel middleware → встроенные метрики Watermill → deserialization guard → Retry → dead letter → handler, [ADR 28](adr/0028-selective-use-of-watermill-cqrs.md)); `pkg/telemetry` поднимает MeterProvider, OTLP exporter и Prometheus bridge, а `KafkaConsumer` выполняет финальный export после drain. Без `OTEL_EXPORTER_OTLP_ENDPOINT` метрики не экспортируются. Grafana dashboard [9777](https://grafana.com/grafana/dashboards/9777) — референс панелей row `Kafka` в overview `orders`, а не отдельный dashboard.

В `orders` тот же `pkg/telemetry` используют все процессы: каждый lifecycle-worker (`App`, `TemporalWorker`, `KafkaConsumer`) поднимает pipeline в `Start` и выполняет финальный export после drain. `Start` регистрирует глобальный W3C propagator (`tracecontext` и `baggage`), поэтому `otelgrpc` продолжает trace вызывающего из `traceparent`. TracerProvider с OTLP gRPC exporter и `BatchSpanProcessor` создаётся, только если задан `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` или `OTEL_EXPORTER_OTLP_ENDPOINT`; иначе traces не экспортируются. Traces и metrics используют один resource, а shutdown выполняет финальный export обоих провайдеров. Pipeline добавляет Go runtime metrics и `process.cpu.time`; host-метрики `system.*` отбрасываются View. gRPC server и client инструментируются stats handler `otelgrpc` по актуальным semconv RPC (`rpc.server.call.duration`, `rpc.client.call.duration` в секундах, статус — имя кода в `rpc.response.status_code`). Temporal SDK пишет в тот же pipeline через `MetricsHandler` с монотонными counters; histograms Temporal получают границы buckets в секундах через View.

### Use cases

Application use cases наблюдаются декоратором handler в composition root, а не кодом use case. В `orders` это `telemetry.Instrument` из `pkg/telemetry`, а имена use cases и классификация ошибок — `slices/<slice>/infrastructure/observability`. Adapters (gRPC, Watermill, Temporal Activities) зависят от порта `application.Handler[In, Out]`, поэтому декоратор подключается без изменения use case. Другие runtime реализуют тот же contract своими средствами.

| Сигнал | Contract |
| --- | --- |
| Metric | Один histogram `nomnom.usecase.duration` (s) на все use cases → `nomnom_usecase_duration_seconds`; частота вызовов — его `_count`. |
| Labels | `nomnom.usecase.name` — стабильный snake_case (`place_order`); `nomnom.usecase.kind` — `command` \| `query`; `nomnom.outcome` — `success` \| `rejected` \| `error`; `error.type` (semconv) — закрытый набор slice, только при неуспехе. |
| Span | `usecase <name>` с теми же attributes. Status `Error` — только при `nomnom.outcome=error`, описание — `error.type`. |
| Запрещено | Отдельная метрика на каждый use case, ID, subject, payload и текст ошибки в labels/attributes/span events: текст ошибки может содержать вход. |

`rejected` — ожидаемый отказ по правилам use case: невалидный вход, запрет доступа, отсутствующий ресурс, недопустимый переход FSM, доменное правило. `error` — технический сбой: инфраструктура, конфликт версий для retry, отмена и неклассифицированная ошибка. Отмена контекста классифицируется до slice и всегда даёт `error` (`canceled` / `deadline_exceeded`). Неизвестная ошибка — `error/internal`, поэтому новый доменный отказ без классификации виден как сбой и требует дополнить classifier. Для use case отказ authorization backend неотличим от `permission_denied`: `application.FailClosed` намеренно сводит его к запрету. Метрики `nomnom.authz.*` пишет adapter SpiceDB ниже fail-closed, поэтому там `denied`, `timeout` и `error` различаются.

Декоратор измеряет попытку вызова, а не бизнес-результат: повтор через idempotency key (`Replayed`) или Inbox duplicate — тоже `success`. Бизнес-счётчики (раздел «Domain metrics» инвентаря) меняются в точке подтверждённого перехода, не здесь.

## Dashboards

Каждый deployable service владеет одним overview-dashboard `<Service> / Overview` рядом со своими ops-файлами; решение — [ADR 29](adr/0029-grafana-dashboards-as-code.md).

| Service | Dashboard uid | service.name | Файл |
| --- | --- | --- | --- |
| `orders` | `nomnom-orders-overview` | `orders`, `orders-worker`, `orders-consumer` | [overview.json](../boundaries/orders/ops/grafana/dashboards/overview.json) |
| `restaurants` | `nomnom-restaurants-overview` | `restaurants` | [overview.json](../boundaries/restaurants/ops/grafana/dashboards/overview.json) |
| `delivery` | `nomnom-delivery-overview` | `delivery` | [overview.json](../boundaries/delivery/ops/grafana/dashboards/overview.json) |
| `payments` | `nomnom-payments-overview` | `payments` | [overview.json](../boundaries/payments/ops/grafana/dashboards/overview.json) |
| BFF | `nomnom-bff-overview` | `bff` | [overview.json](../boundaries/bff/ops/grafana/dashboards/overview.json) |

| | |
| --- | --- |
| Toolchain | Grafana Foundation SDK (Go) в [`ops/grafana`](../ops/grafana/README.md); `overview.ServiceOverview` задаёт общую структуру rows; JSON — артефакт, `make grafana-check` в CI падает при расхождении. |
| Схема | `dashboard.grafana.app/v2`, Grafana 13.2.4; миграция v1 → v2 для self-managed необратима. |
| Доставка | Git Sync: read-only `Repository` на сервис в [`ops/grafana/git-sync`](../ops/grafana/git-sync). |
| Проверка | `gcx dev lint run` закоммиченных и live-вариантов, тесты генератора. |
| Метрики | Только из [инвентаря](observability/metric-inventory.md) со статусом «есть»; остальные панели — TODO со ссылкой на инвентарь. |

Overview отвечает на вопросы incident: работает ли сервис, выросли ли ошибки (transport, application и business rejection раздельно; PaymentDeclined — не ошибка) и latency, какая зависимость деградировала, есть ли backlog, что с Inbox/Outbox и бизнес-потоком, где trace/log/profile. Серверные метрики PostgreSQL, MongoDB и Kafka остаются в community dashboards, на которые ведут ссылки. Alert rules генерирует тот же генератор только на метриках «есть» ([ADR 29](adr/0029-grafana-dashboards-as-code.md)); SLO, error budget и burn rate alerts — [ADR 40](adr/0040-service-slo-and-burn-rate-alerts.md).

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

Общие соглашения реализаций (`orders` — `pkg/telemetry/logging.go`, `payments` — `pkg/telemetry/logs.py`, `restaurants` — `pkg/telemetry/logger.ts`, `delivery` — `pkg/telemetry/log.rs`): ключи плоские с точками, как resource attributes (`service.name`, `deployment.environment.name`); `severity` — `DEBUG` | `INFO` | `WARN` | `ERROR` | `FATAL`; операция — `operation` (и `outcome`, где есть результат); ошибка — `error.type` (стабильный код или класс), `error.message` (после redaction), stack — отдельным полем (`error.stack` или `exception`). Логи библиотек (gRPC, Watermill, Temporal, librdkafka, sqlx) идут через тот же logger. Уровень `orders` задаёт `LOG__LEVEL`. Alloy читает stdout pods и переводит запись в OTLP (`ops/kind/manifests/observability/alloy.yaml`): `service.*` — resource, `severity`, `trace_id`, `span_id` — поля записи.

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
- переход из метрики к trace через exemplars, если SDK, pipeline и тип метрики сохраняют exemplar;
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
- SLO входа сервисов приняты [ADR 40](adr/0040-service-slo-and-burn-rate-alerts.md); SLO асинхронных путей, recording rules и capacity planning определяются отдельными изменениями после появления рабочих сигналов и ожидаемой нагрузки; overview-dashboards определены [ADR 29](adr/0029-grafana-dashboards-as-code.md).

## Отложенные решения

- количество экземпляров Alloy, agent/gateway topology и способ развёртывания;
- конкретные processors, batch sizes, retry/WAL settings, TLS и authentication между компонентами;
- SDK-библиотеки и закреплённые версии для каждого runtime;
- OTLP endpoint details, temporality и точное преобразование resource attributes;
- sampling, retention, multi-tenancy, storage sizing и disaster recovery;
- Loki labels/structured metadata limits и histogram strategy метрик;
- alerts, SLO и доступ к данным;
- BFF runtime и соответствующая profiling-интеграция.

## Официальные источники

- [OpenTelemetry concepts and signals](https://opentelemetry.io/docs/concepts/signals/), [resources](https://opentelemetry.io/docs/concepts/resources/) и [semantic conventions](https://opentelemetry.io/docs/specs/semconv/)
- [W3C Trace Context](https://www.w3.org/TR/trace-context/) и [OpenTelemetry context propagation](https://opentelemetry.io/docs/concepts/context-propagation/)
- [Grafana Alloy OpenTelemetry](https://grafana.com/docs/alloy/latest/collect/opentelemetry-data/), [Prometheus metrics](https://grafana.com/docs/alloy/latest/collect/prometheus-metrics/) и [Loki ingestion with Alloy](https://grafana.com/docs/loki/latest/send-data/alloy/)
- [Tempo](https://grafana.com/docs/tempo/latest/), [Loki labels](https://grafana.com/docs/loki/latest/get-started/labels/) и [Pyroscope SDK path through Alloy](https://grafana.com/docs/pyroscope/latest/configure-client/grafana-alloy/receive_profiles/)
- [Pyroscope profile types](https://grafana.com/docs/pyroscope/latest/configure-client/profile-types/), [supported platforms](https://grafana.com/docs/pyroscope/latest/configure-client/supported-platforms/) и [Span Profiles](https://grafana.com/docs/pyroscope/latest/configure-client/trace-span-profiles/)
- [Grafana trace correlations](https://grafana.com/docs/grafana/latest/datasources/tempo/configure-tempo-data-source/#trace-to-logs) и [exemplars](https://grafana.com/docs/grafana/latest/fundamentals/exemplars/)
- [Temporal Go observability](https://github.com/temporalio/documentation/blob/main/docs/develop/go/platform/observability.mdx#tracing), [Watermill message context and metadata](https://watermill.io/docs/message/) и [`watermill-kafka` metadata/header mapping](https://github.com/ThreeDotsLabs/watermill-kafka/blob/master/pkg/kafka/marshaler.go)

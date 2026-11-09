# 9. Использовать OpenTelemetry и Grafana observability stack

Date: 2026-10-24

## Status

Accepted

## Context

nomnom состоит из BFF, API и workers на Go, TypeScript/Node.js, Rust и Python. Синхронные gRPC-вызовы, Kafka, Temporal, PostgreSQL и MongoDB образуют цепочки, которые невозможно диагностировать только локальными логами отдельного процесса. Нужны единый контекст и согласованные traces, metrics, logs и continuous profiles без зависимости доменной модели от observability SDK.

Один универсальный backend для всех сигналов упростил бы список компонентов, но ухудшил бы специализированные запросы и эксплуатацию каждого типа данных. Одновременно отдельный collector для каждого сигнала создал бы несколько точек конфигурации, буферизации и контроля доставки.

На этом шаге фиксируется целевая архитектура документации. Исходный код, зависимости, конфигурация Alloy, Docker Compose, Kubernetes и физическая топология не добавляются.

## Decision

Использовать OpenTelemetry API, SDK, semantic conventions и W3C Trace Context для инструментирования приложений, создания traces и metrics и распространения контекста. Логи по умолчанию выводятся структурированным JSON в stdout. Continuous profiles создаются подходящим для runtime Pyroscope SDK.

Использовать Grafana Alloy как единый логический инструмент приёма, сбора, обработки и отправки телеметрии. Количество экземпляров Alloy и способ развёртывания определяются отдельно. Отдельный Prometheus server не добавляется: Alloy выполняет scrape Prometheus-compatible endpoints, а метрики хранит Grafana Cloud Metrics.

Использовать четыре специализированных хранилища:

- Grafana Tempo для distributed traces;
- Grafana Cloud Metrics для метрик и PromQL;
- Grafana Loki для структурированных логов;
- Grafana Pyroscope для continuous profiles.

Grafana предоставляет data sources, dashboards, Explore и переходы между сигналами. Выбор четырёх backend сохраняет нативную модель и язык запросов каждого сигнала и позволяет независимо задавать sampling, retention и стоимость хранения. Цена выбора — больше компонентов, интеграционных связей, политик доступа, capacity planning и процедур обновления.

Потоки данных определены так:

| Сигнал | Путь |
| --- | --- |
| Traces | OpenTelemetry SDK → OTLP → Alloy → OTLP → Tempo |
| Метрики приложений | OpenTelemetry SDK → OTLP → Alloy → OTLP/HTTP → Grafana Cloud Metrics |
| Метрики инфраструктуры | exporter или `/metrics` → Alloy scrape → remote write → Grafana Cloud Metrics |
| Метрики компонентов без SDK (Temporal server) | OBI → OTLP → Alloy → OTLP/HTTP → Grafana Cloud Metrics |
| Database Observability PostgreSQL | Alloy `database_observability.postgres` → метрики: OTLP/HTTP → Grafana Cloud Metrics, query samples/explain plans/схема: `loki.write` → Loki |
| Logs | JSON stdout → Alloy → Loki |
| Profiles | Pyroscope SDK → Alloy `pyroscope.receive_http` → `pyroscope.write` → Pyroscope |

Profiles имеют отдельный путь, потому что Pyroscope SDK и протокол профилей не считаются взаимозаменяемыми с OTLP traces/metrics. Один источник метрик или логов получает один основной путь доставки.

Все сигналы используют общую идентификацию через `service.namespace=nomnom`, стабильный `service.name`, `service.version`, `deployment.environment.name` и, при необходимости, `service.instance.id`. Индивидуальные `order_id`, `user_id`, `workflow_id` и `trace_id` не становятся metric labels или индексируемыми Loki labels.

Процесс настраивает OpenTelemetry из файла конфигурации ([ADR 17](0017-cloud-native-application-runtime.md)), а не из `OTEL_*`: `spec.telemetry` задаёт OTLP endpoint, протокол (`grpc` или `http/protobuf`), `service.name` и resource attributes окружения, `spec.tracing.enabled` и `spec.metrics.enabled` явно включают экспорт traces и metrics, `spec.metrics.exportInterval` — интервал экспорта, `spec.logging.level` — уровень логов. Endpoint без флагов экспорт не включает, а включённый экспорт без endpoint — ошибка старта. `service.instance.id` — имя pod из Downward API (`POD_NAME`).

Инструментирование транспорта, БД, Kafka и внешних клиентов относится к `infrastructure/`. Use cases наблюдаются через декораторы и application ports. SDK providers/exporters, logger и profiler собираются через принятый DI в composition root, а process-level lifecycle находится в bootstrap. `domains/`, включая `vo/`, остаются независимыми от observability SDK.

Подробные правила потоков, tracing, cardinality, redaction, correlations и эксплуатации описаны в [документе observability](../observability.md).

### eBPF-инструментирование

OpenTelemetry eBPF Instrumentation (OBI) дополняет SDK и не заменяет его: propagation через Outbox, Temporal spans и бизнес-метрики снаружи процесса не наблюдаются. OBI выбирает только перечисленные компоненты без SDK, сервисы с SDK не инструментирует. Сейчас это Temporal server: RED-метрики gRPC и связи между сервисами. Режим — только метрики, без traces и propagation, без агента в процессе и без `SYS_ADMIN`.

Компонент добавляется, только если он даёт данные, которых нет в его метриках, и окупает series квоты. Замер на kind (2026-11-01, стенд без нагрузки): Temporal — 651 series; с SQL-метриками было больше 1400 при СУБД `other_sql`, поэтому SQL выключен. Kafka Connect не включён: процесс подхватывается, но метрик нет ни без `SYS_ADMIN`, ни с ним; причина не найдена.

### Database Observability

Серверная сторона PostgreSQL собирается приложением Grafana Cloud Database Observability: Alloy `database_observability.postgres` читает `pg_stat_statements`, `pg_stat_activity`, каталоги и планы запросов. Компонент один на сервер, а не на базу владельца: коллекторы обходят все базы сервера сами. Его встроенный postgres_exporter заменяет отдельные `prometheus.exporter.postgres`, метрики идут общим OTLP-путём с `job="integrations/db-o11y"`. Логи компонента — исключение из пути Logs: приложение ищет их по index label `job`, а OTLP делает labels записи structured metadata, поэтому они пишутся `loki.write` прямо в Loki.

Client span PostgreSQL несёт параметризованный текст запроса (`db.query.text`, placeholders `$n`, значения — bind parameters) и `db.namespace`. По ним Grafana связывает span с запросом в Database Observability (trace linkage по тексту). sqlcommenter (`traceparent` в комментарии SQL) для точного совпадения не используется: уникальный текст каждого выполнения ломает кеш prepared statements pgx и sqlx, несовместим с `sqlx::query!`, а Alloy видит только выполнения, попавшие в опрос раз в 10 секунд.

### Поддержка языков

OpenTelemetry и profiling-интеграции выбираются и проверяются отдельно для каждого runtime. Для `orders` используется Go instrumentation, включая поддерживаемую Temporal SDK интеграцию; рекомендуемая Temporal OTel v2 интеграция на дату решения имеет pre-release статус, поэтому package/version и совместимость history фиксируются при реализации. Watermill переносит W3C-поля через message metadata и Kafka headers. Для continuous CPU profiling планируются Go SDK, Node.js SDK/agent, Rust client с pprof backend и Python SDK/agent соответственно.

Нельзя предполагать одинаковые типы profiles, overhead, platform/runtime compatibility и точную span-to-profile correlation для Go, Node.js, Rust и Python. Из языков проекта официальные Span Profiles документированы для Go и Python и только для CPU; Node.js и Rust ограничиваются корреляцией по service/version/time. Особенно отдельно проверяются community/native зависимости Rust, native profiler Node.js и поддержка целевого Python 3.14. BFF получает profiling-подход после выбора его runtime.

Exemplars (переход metric → trace) так же зависят от SDK. Go и Python SDK пишут их по умолчанию: фильтр `trace_based` сохраняет exemplar измерения внутри sampled span, отдельной настройки и `OTEL_METRICS_EXEMPLAR_FILTER` не нужно. JS SDK (`@opentelemetry/sdk-metrics` 3.x) exemplars не реализует, Rust SDK (`opentelemetry_sdk` 0.33) пишет точки без них — для `restaurants` и `delivery` переход к trace идёт только через `trace_id` логов, собственная реализация reservoir не пишется. Exemplar появляется только у измерения с context активного span: метрики, записанные без context (Temporal `MetricsHandler`) или пришедшие через Prometheus bridge (Watermill), exemplars не несут.

### Отложено до реализации

- закреплённые версии SDK и конкретные библиотеки инструментирования;
- число и topology экземпляров Alloy, processors, queues, retries, WAL, TLS и authentication;
- sampling и retention по окружениям, storage sizing и multi-tenancy;
- набор Loki labels, promoted resource attributes метрик и histogram buckets;
- dashboards, alerts, SLO и правила доступа;
- BFF runtime и поддерживаемая им profiling-интеграция.

## Consequences

Появляется единый сквозной контекст и согласованный путь каждого сигнала. Grafana сможет связывать logs с traces, exemplars с traces и service/time range с profiles. Специализированные backend сохраняют подходящие модели хранения и запросов, а Alloy централизует сбор без отдельного Prometheus server.

Стек требует эксплуатации Alloy, Tempo, Loki, Pyroscope и Grafana, а метрики хранит Grafana Cloud, контроля совместимости протоколов, собственных метрик pipeline, ограничения очередей и осознанной cardinality. Отказ экспорта не должен останавливать бизнес-запросы, поэтому при заполнении ограниченных буферов возможна контролируемая потеря telemetry.

OBI требует DaemonSet с расширенными правами: `hostPID`, hostPath и capabilities, в том числе `BPF`, `PERFMON`, `SYS_PTRACE`, поэтому агент видит процессы всего узла. Ему нужны Linux с BTF, ядро 5.8+ и память на всплеск при старте (пик около 317 МБ при рабочих 100 МБ). Совместимость propagation OBI с eBPF-программами Cilium не проверена: перед включением propagation её нужно проверить отдельно.

Разная зрелость SDK и profilers по языкам ограничивает единообразие. Trace/profile correlation и exemplars включаются только там, где они документированы и проверены. Head sampling не гарантирует сохранение всех ошибочных traces; tail sampling, если понадобится, потребует отдельного решения и дополнительных ресурсов.

Архитектурные границы сохраняются: observability остаётся инфраструктурной возможностью и не создаёт зависимость доменной модели от SDK. Реализация потребует end-to-end проверки каждого потока и корреляции, потому что наличие атрибута в приложении само по себе не гарантирует его сохранение collector и backend.

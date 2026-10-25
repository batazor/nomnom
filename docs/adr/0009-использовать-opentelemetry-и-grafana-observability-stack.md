# 9. Использовать OpenTelemetry и Grafana observability stack

Date: 2026-10-24

## Status

Accepted

## Context

BiteRoute состоит из BFF, API и workers на Go, TypeScript/Node.js, Rust и Python. Синхронные gRPC-вызовы, Kafka, Temporal, PostgreSQL и MongoDB образуют цепочки, которые невозможно диагностировать только локальными логами отдельного процесса. Нужны единый контекст и согласованные traces, metrics, logs и continuous profiles без зависимости доменной модели от observability SDK.

Один универсальный backend для всех сигналов упростил бы список компонентов, но ухудшил бы специализированные запросы и эксплуатацию каждого типа данных. Одновременно отдельный collector для каждого сигнала создал бы несколько точек конфигурации, буферизации и контроля доставки.

На этом шаге фиксируется целевая архитектура документации. Исходный код, зависимости, конфигурация Alloy, Docker Compose, Kubernetes и физическая топология не добавляются.

## Decision

Использовать OpenTelemetry API, SDK, semantic conventions и W3C Trace Context для инструментирования приложений, создания traces и metrics и распространения контекста. Логи по умолчанию выводятся структурированным JSON в stdout. Continuous profiles создаются подходящим для runtime Pyroscope SDK.

Использовать Grafana Alloy как единый логический инструмент приёма, сбора, обработки и отправки телеметрии. Количество экземпляров Alloy и способ развёртывания определяются отдельно. Отдельный Prometheus server не добавляется: Alloy выполняет scrape Prometheus-compatible endpoints, а хранение метрик обеспечивает Mimir.

Использовать четыре специализированных хранилища:

- Grafana Tempo для distributed traces;
- Grafana Mimir для метрик и PromQL;
- Grafana Loki для структурированных логов;
- Grafana Pyroscope для continuous profiles.

Grafana предоставляет data sources, dashboards, Explore и переходы между сигналами. Выбор четырёх backend сохраняет нативную модель и язык запросов каждого сигнала и позволяет независимо задавать sampling, retention и стоимость хранения. Цена выбора — больше компонентов, интеграционных связей, политик доступа, capacity planning и процедур обновления.

Потоки данных определены так:

| Сигнал | Путь |
| --- | --- |
| Traces | OpenTelemetry SDK → OTLP → Alloy → OTLP → Tempo |
| Метрики приложений | OpenTelemetry SDK → OTLP → Alloy → OTLP/HTTP → Mimir |
| Метрики инфраструктуры | exporter или `/metrics` → Alloy scrape → remote write → Mimir |
| Logs | JSON stdout → Alloy → Loki |
| Profiles | Pyroscope SDK → Alloy `pyroscope.receive_http` → `pyroscope.write` → Pyroscope |

Profiles имеют отдельный путь, потому что Pyroscope SDK и протокол профилей не считаются взаимозаменяемыми с OTLP traces/metrics. Один источник метрик или логов получает один основной путь доставки.

Все сигналы используют общую идентификацию через `service.namespace=nomnom`, стабильный `service.name`, `service.version`, `deployment.environment.name` и, при необходимости, `service.instance.id`. Индивидуальные `order_id`, `user_id`, `workflow_id` и `trace_id` не становятся metric labels или индексируемыми Loki labels.

Инструментирование транспорта, БД, Kafka и внешних клиентов относится к `infrastructure/`. Use cases наблюдаются через декораторы и application ports. SDK providers/exporters, logger и profiler собираются через принятый DI в composition root, а process-level lifecycle находится в bootstrap. `domains/`, включая `vo/`, остаются независимыми от observability SDK.

Подробные правила потоков, tracing, cardinality, redaction, correlations и эксплуатации описаны в [документе observability](../observability.md).

### Поддержка языков

OpenTelemetry и profiling-интеграции выбираются и проверяются отдельно для каждого runtime. Для `orders` используется Go instrumentation, включая поддерживаемую Temporal SDK интеграцию; рекомендуемая Temporal OTel v2 интеграция на дату решения имеет pre-release статус, поэтому package/version и совместимость history фиксируются при реализации. Watermill переносит W3C-поля через message metadata и Kafka headers. Для continuous CPU profiling планируются Go SDK, Node.js SDK/agent, Rust client с pprof backend и Python SDK/agent соответственно.

Нельзя предполагать одинаковые типы profiles, overhead, platform/runtime compatibility и точную span-to-profile correlation для Go, Node.js, Rust и Python. Из языков проекта официальные Span Profiles документированы для Go и Python и только для CPU; Node.js и Rust ограничиваются корреляцией по service/version/time. Особенно отдельно проверяются community/native зависимости Rust, native profiler Node.js и поддержка целевого Python 3.15. BFF получает profiling-подход после выбора его runtime.

### Отложено до реализации

- закреплённые версии SDK и конкретные библиотеки инструментирования;
- число и topology экземпляров Alloy, processors, queues, retries, WAL, TLS и authentication;
- sampling и retention по окружениям, storage sizing и multi-tenancy;
- набор Loki labels, promoted Mimir resource attributes и histogram buckets;
- dashboards, alerts, SLO и правила доступа;
- BFF runtime и поддерживаемая им profiling-интеграция.

## Consequences

Появляется единый сквозной контекст и согласованный путь каждого сигнала. Grafana сможет связывать logs с traces, exemplars с traces и service/time range с profiles. Специализированные backend сохраняют подходящие модели хранения и запросов, а Alloy централизует сбор без отдельного Prometheus server.

Стек требует эксплуатации Alloy, Tempo, Mimir, Loki, Pyroscope и Grafana, контроля совместимости протоколов, собственных метрик pipeline, ограничения очередей и осознанной cardinality. Отказ экспорта не должен останавливать бизнес-запросы, поэтому при заполнении ограниченных буферов возможна контролируемая потеря telemetry.

Разная зрелость SDK и profilers по языкам ограничивает единообразие. Trace/profile correlation включается только там, где она документирована и проверена. Head sampling не гарантирует сохранение всех ошибочных traces; tail sampling, если понадобится, потребует отдельного решения и дополнительных ресурсов.

Архитектурные границы сохраняются: observability остаётся инфраструктурной возможностью и не создаёт зависимость доменной модели от SDK. Реализация потребует end-to-end проверки каждого потока и корреляции, потому что наличие атрибута в приложении само по себе не гарантирует его сохранение collector и backend.

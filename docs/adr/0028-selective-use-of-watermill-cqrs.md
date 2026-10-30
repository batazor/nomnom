# 28. Selective Use of Watermill CQRS

Date: 2026-10-25

## Status

Accepted

Связан с [4. Использовать Event-Driven Architecture и Apache Kafka](0004-use-event-driven-architecture-with-apache-kafka.md), [6. DDD Structure Inside Vertical Slices](0006-ddd-structure-inside-vertical-slices.md), [11. Transactional Inbox and Idempotent Event Processing](0011-transactional-inbox-and-idempotent-event-processing.md), [13. Transactional Outbox for Integration Event Publishing](0013-transactional-outbox-for-integration-event-publishing.md), [17. Cloud Native Application Runtime](0017-cloud-native-application-runtime.md), [20. Unit of Work for Local Transactions](0020-unit-of-work-for-local-transactions.md), [25. Integration Event Publication](0025-integration-event-publication.md)

Связан с ADR `orders` [3. Использовать Watermill для событийной интеграции](../../boundaries/orders/docs/adr/0003-use-watermill-for-event-integration-in-orders.md) и [4. Использование CQRS в orders](../../boundaries/orders/docs/adr/0004-использование-cqrs-в-orders.md)

## Context

Go-контекст `orders` использует Watermill для Kafka ([ADR orders 3](../../boundaries/orders/docs/adr/0003-use-watermill-for-event-integration-in-orders.md)) и CQRS на уровне application ([ADR orders 4](../../boundaries/orders/docs/adr/0004-использование-cqrs-в-orders.md)). Watermill предлагает собственный [компонент CQRS](https://watermill.io/docs/cqrs/): `CommandBus`, `CommandProcessor`, `EventBus`, `EventProcessor`, `EventGroupProcessor` и marshaler. Совпадение названий провоцирует ошибки:

- провести локальные Commands/Queries через `CommandBus` и Kafka, превратив синхронный use case в асинхронный;
- публиковать события через `EventBus.Publish()` мимо Outbox ([ADR 13](0013-transactional-outbox-for-integration-event-publishing.md));
- полагаться на дефолтный marshaler (имя Go-структуры), несовместимый с publisher'ами на Python, TypeScript и Rust;
- получить топик на каждый тип события (дефолт `EventProcessor`) и потерять порядок событий одного aggregate;
- превратить business error в бесконечный transport retry.

[ADR 6](0006-ddd-structure-inside-vertical-slices.md#входы-в-application) уже запрещает in-process command bus по умолчанию; adapter вызывает handler напрямую через DI.

На этом шаге фиксируются документация и решение. Код и зависимости не добавляются.

## Decision

### Application CQRS ≠ Watermill CommandBus

Локальный CQRS сохраняется: `Command/Query → Application Handler → Domain/Read Model`. Commands и Queries — локальные typed inputs, handler получают через DI. Они не проходят через Watermill. Цепочка `HTTP/gRPC → CommandBus → Kafka → CommandProcessor` для синхронных use cases запрещена.

### Watermill CQRS = typed event dispatch для Kafka integration

Из Watermill CQRS используется только `EventProcessor` (в форме handler groups) для входящих integration events:

```text
Kafka → Watermill Subscriber → EventProcessor → Watermill handler (infrastructure)
→ deserialize envelope/DTO → map to Command → Application Handler
→ Inbox (insert-first) → UoW → Domain/FSM → Outbox → commit → return nil → ack
```

- Ack — только после commit UoW.
- Watermill handler — infrastructure adapter: десериализация, mapping в Command, вызов handler'а, классификация ошибки. Domain logic в нём нет.
- Application Handler не зависит от Watermill. `domains` не знает Watermill, Kafka, `EventProcessor`, `EventBus`, `CommandBus` и `Router`.
- Inbox обязателен ([ADR 11](0011-transactional-inbox-and-idempotent-event-processing.md)); порядок insert-first — по [Unit of Work](../unit-of-work.md).

Другие языки используют свои consumer-реализации по тем же правилам: dispatch по `event_type`, Inbox, ack после commit, классификация ошибок.

### Kafka-клиент

- Официальный `watermill-kafka/v3` поверх IBM/sarama. Community-реализации на franz-go (`dubyte/watermill-kafka-franz` и форки) без измеренной причины не используются; это open option.
- `sarama.Config.ClientID` — `service.name` процесса (например, `orders-consumer`), а не дефолт `watermill`: по нему брокер применяет квоты и ведёт диагностику.
- Дефолт Watermill `Consumer.Offsets.Initial = OffsetOldest`: новая consumer group читает стрим с начала. Для каждой группы это решается явно (`oldest` — replay истории, например для новой проекции; `newest` — только новые события) и документируется. Иначе первый деплой нового consumer'а переобработает всю историю через Inbox. Группа `orders` — `oldest`: события по уже существующим `Order` должны быть отражены, повтор идемпотентен.
- Трассировка Kafka-слоя — `Tracer: kafka.NewOTELSaramaTracer()` в `PublisherConfig` и `SubscriberConfig` (otelsarama: spans и `traceparent` в headers). Metadata ↔ headers маппит `DefaultMarshaler`; header `_watermill_message_uuid` зарезервирован и в envelope не используется. Publishers на других языках его не ставят, поэтому UUID входящего сообщения — `event_id`.
- Partitioning — `kafka.NewWithPartitioningMarshaler`, ключ — `aggregate_id` из metadata. Forwarder публикует исходное сообщение после unwrap своего envelope, поэтому ключ читается уже из него.

### Marshaler

Используется custom `CommandEventMarshaler`:

- имя события — `event_type` Protobuf envelope (или header, по [ADR 25](0025-integration-event-publication.md)), то есть полное имя Protobuf-сообщения, например `nomnom.restaurants.v1.RestaurantOrderAccepted`;
- payload — Protobuf;
- `event_id` и trace context переносятся из envelope/headers в metadata сообщения.

Дефолтный marshaler по имени Go-структуры не используется: события публикуют контексты на других языках.

### Топики, ordering, handler groups

- Топик — stream aggregate контекста-источника, например `restaurants.v1.restaurant-order-events`. Ключ — `aggregate_id`.
- Типы событий внутри стрима маршрутизируются по `event_type`.
- Handlers одного стрима регистрируются одной handler group с общим subscriber'ом: так сохраняется порядок событий одного aggregate.
- Топик-на-тип-события (дефолт `EventProcessor`) не используется.
- Ordering ≠ idempotency. `aggregate_version` проверяет application, если сценарию нужен порядок.
- Конкретные имена топиков и partitioning — integration specification.

### Ошибки

| Класс | Источник | Действие handler'а |
| --- | --- | --- |
| Deserialization (битый record, невалидный envelope) | subscriber unmarshaler | `<stream>.dlq` + ack до любого handler |
| Unknown `event_type`, permanent | infrastructure | `<stream>.dlq` + ack |
| Transient (DB, network) | infrastructure / application | Retry middleware с backoff и лимитом; после исчерпания — nack: partition ждёт, Kafka доставляет повторно |
| Business error | domain / application | ack; результат — через outbox-событие или лог, не retry |
| Duplicate (Inbox) | application | ack, no-op |

Watermill не превращает business errors в бесконечный transport retry. Исчерпанный transient не уходит в DLQ: зависимость (БД, Temporal) временно недоступна, а не сообщение плохое; head-of-line blocking виден по `nomnom.messaging.retry.attempts` и consumer lag. DLQ — топик `<stream>.dlq` на каждый stream; в metadata — причина, исходный топик и consumer group, потому что один стрим могут читать несколько контекстов. Разбор — [ниже](#восстановление-из-dlq).

#### Восстановление из DLQ

В DLQ попадает то, что повтором не лечится: дефект кода или контракта. Поэтому автоматического повтора из DLQ (retry topic, повтор по таймеру) нет: без исправления запись вернётся туда же. Разбирает оператор после исправления; runbook — [Разбор DLQ](../dlq.md).

- `<stream>.dlq` объявляется окружением явно, до consumers, а не auto create при первом dead letter: `retention.ms` — 30 дней (окно разбора), `cleanup.policy=delete`, одна partition (разбор идёт по порядку целиком, ключ `aggregate_id` сохраняется в записи). Consumer DLQ не создаёт. В kind это Job `dead-letter-topics` (`ops/kind/manifests/messaging/dead-letter-topics.yaml`), в production — provisioning topics окружения с теми же параметрами.
- Каждая consumer group разбирает только свои dead letters (`consumer_group_poisoned`). Что разобрано, хранит Kafka consumer group `<group>.dlq`: её committed offset на `<stream>.dlq` — первая неразобранная запись. Отдельного хранилища состояния разбора нет.
- Неразобранное — конец partition DLQ минус offset `<group>.dlq`, а у partition без commit группы — минус начало topic (группа разбирает с начала). Метрики — Kafka exporter (`kafka_topic_partition_current_offset`, `kafka_topic_partition_oldest_offset`, `kafka_consumergroup_current_offset`); alert `<context>-kafka-dlq-unresolved` срабатывает, когда записи не разобраны сутки. Записи других групп того же DLQ входят в оценку сверху; alert `kafka-poison` по-прежнему сигналит о каждом новом dead letter.
- **Redrive** — публикация dead letter обратно в исходный стрим с тем же ключом, payload и envelope headers, без метаданных dead letter и с `redriven_from=<stream>.dlq/<partition>@<offset>`. Событие проходит обычный путь: Router, middleware, Inbox, UoW. Второго pipeline обработки «в обход стрима» нет. Цена — событие снова получают все группы стрима; Inbox делает это no-op там, где оно обработано.
- **Discard** — пометка разобранным без публикации; запись и причина пишутся WARN-логом.
- Redrive доставляет событие после более новых событий агрегата. Это допустимо, потому что ordering ≠ idempotency: use cases, которым нужен порядок, проверяют `aggregate_version` и признают поздние факты.
- Записи разбираются по порядку в partition, offset коммитится после каждой: прерванный разбор продолжается с места остановки.
- Реализация — разовая операторская команда в образе consumer'а (`orders-admin dlq list|redrive|discard`, запуск `kubectl exec` в pod consumer'а с его конфигурацией). Контексты на других языках реализуют тот же контракт своей командой: у `restaurants` — `restaurants-admin dlq` (`boundaries/restaurants/cmd/restaurants-admin`).

#### Десериализация

Ошибка `Unmarshal` в sarama-subscriber'е — fail-fast: сообщение не ack'ается, offset не коммитится, partition стоит. Поэтому `Unmarshaler` никогда не возвращает ошибку: невалидный record становится сообщением с raw payload и metadata `deserialization_error=…`, которое deserialization guard направляет в `<stream>.dlq` и ack'ает. Это проверяется тестом: битый record не блокирует partition. У будущего franz-go клиента для этого есть `OnUnmarshalError`, у sarama-версии — нет.

Value Integration Event — wire format Schema Registry ([ADR 25](0025-integration-event-publication.md#payload-и-schema-registry)). `Unmarshaler` проверяет формат без Registry: value вне его — тот же `deserialization_error`. Value остаётся с префиксом, поэтому dead letter хранит запись как есть и redrive публикует её без изменений; префикс отрезает `EnvelopeMarshaler` перед разбором Protobuf. Subject схемы сверяет отдельный middleware после deserialization guard: id неизвестен или его subjects не содержат `event_type` — `<stream>.dlq` как deserialization; недоступный Registry — nack до Retry, partition ждёт и Kafka доставит запись снова. Id схем неизменяемы, поэтому найденные subjects consumer хранит до конца жизни процесса. `SCHEMA_REGISTRY__URL` обязателен consumer'у: без него процесс не стартует.

#### Порядок middleware

Снаружи внутрь:

```text
Recoverer (panic → error) → OpenTelemetry/metrics → deserialization guard → schema subject → Retry → dead letter (permanent → <stream>.dlq) → unknown event_type → handler
```

`CircuitBreaker` — опционально на handlers с внешними зависимостями вне UoW (SpiceDB, gateway); решение при реализации.

### Outbox — единственный путь публикации

Исходящие события идут только по пути `Domain Event → Application → Transport DTO → Outbox → Watermill Forwarder → Kafka` ([ADR 13](0013-transactional-outbox-for-integration-event-publishing.md), [ADR 25](0025-integration-event-publication.md)). `EventBus.Publish()` до commit запрещён. `EventBus` не является абстракцией для Domain Events: они остаются in-memory. Второго пути публикации рядом с Outbox нет.

### CommandBus и Queries

`CommandBus` и `CommandProcessor` не используются: Commands — локальные typed inputs, синхронные запросы ждут результат, orchestration — Temporal, Kafka — интеграция между контекстами. Применение `CommandBus` для действительно асинхронных команд возможно позже отдельным решением.

Queries выполняются напрямую: `Query Handler → Read Repository/Projection → DTO`, не через Watermill.

### Размещение

```text
<slice>/
  applications/handler.*
  infrastructure/messaging/watermill_handler.*, dto.*, mapper.*
```

`Router`, subscriber, publisher, `EventProcessor`, регистрация handlers, middleware (retry, poison, OpenTelemetry, metrics), lifecycle и graceful shutdown — composition root deployable role consumer (`cmd/<boundary>-consumer`, по аналогии с `cmd/orders-worker`). Router внутри slice не создаётся.

### Observability

- OpenTelemetry middleware продолжает trace context из headers (W3C `traceparent`); spans Kafka-клиента — otelsarama.
- Метрики (имена — инвентарь `ops/grafana/inventory`, секция messaging): `nomnom.messaging.process.duration` (s), `nomnom.messaging.message.age` (s, now − `occurred_at`), `nomnom.messaging.retry.attempts`, `nomnom.messaging.poison.messages`, `nomnom.inbox.duplicates`. Атрибуты — `handler_name`, `event_type`, `stream`; `error_class` — только при неуспехе. `event_id` и `aggregate_id` в labels не используются ([ADR 9](0009-использовать-opentelemetry-и-grafana-observability-stack.md)). Consumer lag измеряется на стороне брокера.
- Логи Watermill — `watermill.NewSlogLogger`, в формате логов сервиса.
- Forwarder — с Watermill 1.5.3 имя handler'а задаётся: `orders-outbox-forwarder`, чтобы `handler_name` в `handler_execution_time_seconds` отличал forwarder от consumer handlers.

### Тестирование

- Unit — application handlers без Watermill.
- Wiring — Watermill `gochannel` Pub/Sub in-memory: marshaler, маршрутизация по `event_type`, handler groups, error mapping.
- Integration — Testcontainers: `Kafka → Watermill → EventProcessor → Inbox → handler`, ordering по ключу, retry и poison.
- E2E — kind ([ADR 14](0014-использовать-kind-для-тестового-окружения-kubernetes.md)).

## Consequences

Входящие integration events получают типизированный dispatch, единые middleware и одинаковое поведение ошибок без ручного switch по `event_type`. Граница между application CQRS и транспортом становится явной: use cases не меняют синхронность из-за выбора библиотеки, публикация остаётся атомарной с state.

Цена — custom marshaler и handler groups вместо дефолтов Watermill, обязательная классификация ошибок в каждом Watermill handler и отдельный процесс consumer на boundary. Business error подтверждается, поэтому application обязан явно зафиксировать результат (outbox-событие или лог), иначе факт отказа теряется. DLQ разбирается вручную после исправления дефекта ([Разбор DLQ](../dlq.md)): redrive повторно доставляет событие всем группам стрима, dead letter, не разобранный за 30 дней retention, теряется (за сутки до этого уже горит alert), а topics DLQ — часть provisioning окружения.

# 13. Transactional Outbox for Integration Event Publishing

Date: 2026-10-24

## Status

Accepted

Дополняет [4. Использовать Event-Driven Architecture и Apache Kafka](0004-use-event-driven-architecture-with-apache-kafka.md)

Связан с [11. Transactional Inbox and Idempotent Event Processing](0011-transactional-inbox-and-idempotent-event-processing.md)

Дополнено [25. Integration Event Publication](0025-integration-event-publication.md)

## Context

[ADR 4](0004-use-event-driven-architecture-with-apache-kafka.md) выбирает интеграционные события и Apache Kafka для асинхронного взаимодействия bounded contexts. Publisher должен одновременно изменить своё локальное состояние и сообщить об этом изменении другим контекстам.

Прямая публикация в Kafka из обработчика является dual write: локальная транзакция и запись в Kafka не фиксируются атомарно.

- Если публиковать до commit, событие может уйти, а транзакция откатиться. Consumers узнают об изменении, которого не было.
- Если публиковать после commit, процесс может упасть между commit и публикацией, или Kafka может быть недоступна. Изменение сохранено, а событие потеряно.

Общей distributed transaction между хранилищем контекста и Kafka в архитектуре нет и не вводится ([ADR 8](0008-storage-strategy-postgresql-and-mongodb.md)). [ADR 11](0011-transactional-inbox-and-idempotent-event-processing.md) фиксирует consumer-сторону: at-least-once delivery и Transactional Inbox. Ему нужен publisher, который не теряет события и назначает им стабильный `event_id`, в том числе когда повторяется не публикация, а сама операция: Temporal Activity retry, повторный HTTP- или gRPC-запрос клиента, повторная команда.

На этом шаге фиксируется только архитектурное решение. Таблицы и коллекции Outbox, миграции, индексы, publisher workers, Kafka topics и producer-код не создаются.

## Decision

### Семантика доставки

Интеграционные события доставляются по модели [Guaranteed Delivery](https://www.enterpriseintegrationpatterns.com/GuaranteedMessaging.html) с семантикой [At-least-once Delivery](https://www.cloudcomputingpatterns.org/at_least_once_delivery/):

```text
publication:
  at-least-once

event identity:
  stable event_id

ordering:
  only within one Kafka partition for the chosen key
```

- Событие, зафиксированное в локальной транзакции publisher, не теряется из-за сбоя процесса между записью данных и публикацией в Kafka или из-за недоступности Kafka.
- Событие может быть опубликовано и обработано повторно. Повторная публикация не является ошибкой протокола.
- Consumers обязаны быть идемпотентными: одно и то же `event_id` приводит к одному и тому же наблюдаемому бизнес-результату. Механизм дедупликации — Transactional Inbox по ADR 11.
- Порядок гарантируется только внутри одной Kafka partition по выбранному partition key.

Exactly-once между сервисами не обещается, и система целиком не называется exactly-once. Гарантии отдельных компонентов, например exactly-once внутри SQL Pub/Sub Watermill, не переносятся на межсервисную доставку.

### Transactional Outbox

Bounded context, публикующий интеграционное событие в результате изменения своего состояния, использует Transactional Outbox:

```text
application handler
    |
    v
begin local transaction
    |
    +--> persist domain/application state
    +--> persist integration event into outbox
    |
commit
    |
    v
outbox publisher --> Kafka --> mark published
```

Изменение state и запись Outbox фиксируются атомарно в одной локальной транзакции хранилища контекста. Если транзакция откатывается, событие не создаётся. Если она зафиксирована, событие будет опубликовано, даже если процесс упал сразу после commit или Kafka была недоступна.

Отдельный Outbox publisher после commit читает неопубликованные записи, публикует их в Kafka и фиксирует факт публикации только после подтверждения Kafka. Факт публикации хранится durable и переживает перезапуск publisher: после рестарта он продолжает с первой неподтверждённой записи.

Запрещены:

- синхронная публикация в Kafka внутри бизнес-транзакции;
- dual write «commit, затем publish» без Outbox;
- прямая публикация интеграционных событий из application handler, repository или domain-кода в обход Outbox.

### Область применения и владение

Outbox применяется к publishers интеграционных событий в `orders`, `restaurants`, `delivery` и `payments`. Каждый сервис владеет своим Outbox в своём основном хранилище; Outbox одного контекста не читается и не используется другим контекстом.

### Domain events и integration events

- Domain Event — факт внутри доменной модели контекста.
- Integration Event — публичное сообщение для других bounded contexts с собственным контрактом.

Domain event не обязан становиться integration event. Application layer решает, какие integration events публикует сценарий, и записывает их в Outbox в той же транзакции, что и локальное изменение. Внутренние доменные события, которые обрабатываются внутри одной транзакционной границы приложения, Outbox не используют. Kafka не является частью доменной модели. Владение контрактом, маппинг domain event → integration event и эволюция контрактов определены в [ADR 25](0025-integration-event-publication.md).

### Запись Outbox

Логическая запись Outbox:

```text
event_id
event_type
aggregate_type
aggregate_id
aggregate_version
occurred_at
payload
metadata
```

Плюс техническое состояние публикации, которое определяется реализацией: например `published_at`, `attempts`, `last_error` или offset consumer group.

- `payload` — Protobuf-сообщение контракта интеграционного события, а не сериализация доменного агрегата или persistence-модели. Размещение, lint и правила совместимости event-схем в Buf определяются вместе с контрактами событий.
- `metadata` содержит W3C trace context, correlation id и причинное событие (`causation_id` — `event_id` входящего события, если запись создана его обработчиком).
- Kafka message key — идентификатор доменной сущности, чей порядок нужен consumers; для заказа — `order_id`.

### Стабильность `event_id`

`event_id` — UUID, который infrastructure детерминированно вычисляет при создании записи Outbox в той же транзакции: UUIDv5 в namespace проекта от `(aggregate_type, aggregate_id, aggregate_version, event_type)`, где `event_type` — полное имя Protobuf-сообщения контракта. Он уникален в хранилище publisher, что обеспечивает storage engine, и не меняется при повторных попытках публикации.

- Конфликт уникальности `event_id` при записи — ошибка: транзакция откатывается, запись молча не пропускается.
- Если одна транзакция создаёт для одного aggregate несколько integration events одного `event_type` или событие не относится к aggregate, кортеж не уникален либо неприменим. Такой сценарий явно задаёт иной способ — например, порядковый номер события в commit или idempotency key операции — и фиксирует его в SDD.
- При маппинге одного факта в несколько integration events, включая dual publish разных major-версий, каждое событие получает свой `event_id`, так как различается `event_type`.

`event_id` передаётся consumer в Kafka header `event_id`. Это тот же идентификатор, по которому Inbox выполняет дедупликацию по ADR 11. Если будущий protobuf envelope события также содержит поле `event_id`, его значение совпадает с header.

Повтор самой операции после успешного commit не должен создавать второй записи Outbox с новым `event_id` для того же логического факта, иначе Inbox на стороне consumer не распознает дубликат. Детерминированный `event_id` этого не гарантирует: повтор, вычисленный против уже изменённого агрегата, получает новую `aggregate_version` и новый `event_id`. Поэтому обязательна и **идемпотентность операции**: команда, которая изменяет state и пишет Outbox, идемпотентна по idempotency key команды или по ожидаемой `aggregate_version`. Повтор уже применённой операции не пишет ни state, ни Outbox и возвращает прежний результат.

| Источник повтора | Ключ идемпотентности операции |
| --- | --- |
| Повторный HTTP/gRPC-запрос клиента | Idempotency key команды, переданный клиентом, вместе с сохранённым результатом |
| Повторная доставка входящего события | `event_id` входящего события и Inbox по ADR 11 |
| Temporal Activity retry в `orders` | Стабильный между попытками ключ, производный от Workflow ID и шага процесса, либо ожидаемая `aggregate_version`, переданная из workflow |
| Повторная внутренняя команда | Явный idempotency key команды либо ожидаемая `aggregate_version` |

Детерминированный `event_id` и идемпотентность операции дополняют друг друга: первый делает идентичность события воспроизводимой из факта (повторное вычисление записи для той же версии даёт тот же `event_id`, а не дубликат с новым), вторая не допускает повторного изменения state.

### Publisher и ordering

Publisher публикует записи в порядке их вставки для одного partition key и не публикует следующую запись ключа, пока предыдущая не подтверждена Kafka. Упавшая публикация не пропускается: это head-of-line blocking, который сопровождается alert'ом. Параллельные экземпляры publisher не должны нарушать порядок.

Poison outbox record — невалидный payload или сообщение, навсегда отклонённое Kafka, — не пропускается молча и не удаляется. Процедура карантина и ручного восстановления определяется отдельно.

Порядок может нарушаться и при одном partition key, например из-за повторной публикации. Поэтому consumers, для которых порядок критичен, могут дополнительно проверять `aggregate_version` по ADR 11.

### `orders`: Go, PostgreSQL и Watermill

- PostgreSQL-база `orders` хранит state и Outbox. Запись агрегата и сообщения Outbox выполняется в одной транзакции: [`watermill-sql`](https://watermill.io/pubsubs/sql/) publisher создаётся на `*sql.Tx` этой транзакции, по одному на транзакцию, без `AutoInitializeSchema`.
- [Watermill Forwarder](https://watermill.io/advanced/forwarder/) доставляет сообщения из SQL Outbox в Kafka через `watermill-kafka`. Kafka остаётся транспортом, SQL Pub/Sub — хранилищем Outbox.
- Forwarder оборачивает исходное сообщение в envelope и при доставке восстанавливает его UUID, payload и metadata. UUID сообщения Watermill равен `event_id`, а значение также записывается в metadata `event_id`, которую marshaler переносит в Kafka header. Consumers на других языках не зависят от служебных headers Watermill.
- Kafka message key передаётся через metadata исходного сообщения и извлекается partitioning marshaler Kafka publisher'а (`kafka.NewWithPartitioningMarshaler`). Без этого ordering по `order_id` не сохраняется: ключ envelope-сообщения в SQL Outbox не попадает в Kafka.
- `AckWhenCannotUnwrap` не включается: подтверждение нераспаковываемого envelope означало бы потерю события. Такая запись считается poison outbox record.
- `watermill-sql` с PostgreSQL offsets adapter блокирует строку consumer group через `FOR UPDATE`, поэтому в каждый момент времени читает один активный reader consumer group. Forwarder работает как single active instance: дополнительные replicas служат failover, а не throughput. Это ограничение масштабирования принимается.
- Собственный polling или outbox framework не реализуется, пока Watermill покрывает требования. Watermill, `watermill-sql` и `watermill-kafka` не импортируются в `domains`.

### TypeScript, Rust и Python

`restaurants` (TypeScript), `delivery` (Rust) и `payments` (Python) соблюдают тот же контракт:

1. атомарно сохранить изменение state и запись Outbox;
2. опубликовать запись в Kafka отдельным worker'ом;
3. повторять публикацию при временном сбое;
4. считать повторную публикацию штатной, а не ошибкой протокола.

Библиотеки не выбираются до определения storage engines и transaction capabilities этих контекстов. `restaurants` использует MongoDB и multi-document транзакцию, охватывающую изменённые документы и коллекцию Outbox. Для ещё не выбранных хранилищ `delivery` и `payments` эквивалентная атомарная запись state и Outbox обязательна и проверяется при выборе.

**Открытый вопрос: polling или CDC.** Способ чтения Outbox для не-Go сервисов — polling worker или CDC (например, [Debezium Outbox Event Router](https://debezium.io/documentation/reference/stable/transformations/outbox-event-router.html), MongoDB change streams) — не выбран. Решение принимается отдельным ADR после выбора хранилищ и должно сохранить ordering по key, head-of-line поведение, стабильный `event_id` и observability этого ADR.

### Связь с Inbox

Если consumer обрабатывает входящее событие и публикует новое, Inbox, business state и Outbox фиксируются в одной локальной транзакции по ADR 11. Повторная доставка входящего события становится no-op и не создаёт вторую запись Outbox.

```text
Transactional Outbox
        +
At-least-once delivery
        +
Transactional Inbox
        =
reliable event processing with effectively-once business effects
```

Эта комбинация не является глобальной distributed transaction.

### VSA и DDD

Сохраняется структура из [ADR 6](0006-ddd-structure-inside-vertical-slices.md):

```text
<slice>/
  domains/        — бизнес-факты и доменные события
  applications/   — транзакционная операция и набор integration events
  infrastructure/ — state + outbox persistence, доставка в Kafka
```

Outbox является инфраструктурным механизмом и не попадает в доменную модель; Kafka и Watermill imports в `domains` запрещены. Application handler определяет атомарную единицу работы сценария и решает, какие integration events создать, передавая их в порт как доменные события или Application DTO. Инфраструктура преобразует их в Protobuf payload, предоставляет транзакцию и включает в неё запись Outbox ([ADR 6](0006-ddd-structure-inside-vertical-slices.md#mapping)). Publisher работает как отдельная инфраструктурная задача запускаемого приложения и собирается в его composition root. Общие компоненты Outbox выносятся в shared infrastructure boundary контекста только при реальном переиспользовании между slices.

### Temporal в `orders`

Temporal retries не заменяют Outbox. Temporal отвечает за durable orchestration, Kafka — за интеграцию между контекстами, Outbox — за переход от локальной транзакции к Kafka.

Детерминированный workflow-код не пишет в Outbox и не публикует события. Изменение состояния `Order` вместе с записью Outbox выполняется в Activity в одной транзакции PostgreSQL-базы `orders`. Повтор Activity и повтор Kafka event — независимые механизмы с независимой идемпотентностью: Activity идемпотентна по правилу из раздела «Стабильность `event_id`», Kafka event дедуплицирует Inbox consumer.

### Observability

Publisher обязан предоставлять:

- число pending outbox records (backlog);
- возраст самой старой неопубликованной записи;
- число попыток и неудачных попыток публикации;
- длительность публикации в Kafka;
- lag от `occurred_at` до подтверждения публикации.

Metric labels — `event_type` и boundary; `event_id` и `aggregate_id` как labels не используются. Head-of-line blocking и рост возраста неопубликованных записей сопровождаются alert'ами. Publisher логирует `event_id`, `event_type` и результат публикации.

Trace context хранится в metadata записи Outbox, а событие не зависит от существования исходного request span: publisher связывает свою публикацию с сохранённым контекстом по [правилам observability](../observability.md#kafka-и-watermill), для длинных асинхронных цепочек — через span links.

### Retention

Опубликованные записи Outbox не хранятся бесконечно. Retention не меньше окна, нужного для диагностики и replay. Очистка не затрагивает неопубликованные записи. Конкретный срок сейчас не задаётся.

Подробности, сценарии сбоев и обязательные fault scenarios описаны в [документации событийного взаимодействия](../event-driven-architecture.md).

## Consequences

Изменение состояния и факт его публикации больше не расходятся: событие не теряется при сбое процесса или недоступности Kafka и не появляется при откате транзакции. Business operation не ждёт Kafka и не падает из-за её временной недоступности; такие сбои превращаются в задержку публикации.

Публикация становится асинхронной: между commit и появлением события в Kafka есть задержка publisher, которую нужно наблюдать. Каждый publisher получает дополнительную запись в транзакции, worker, retention и очистку Outbox, а также метрики backlog и возраста неопубликованных записей. Хранилище publisher должно поддерживать атомарную запись state и Outbox; для MongoDB это требует транзакций, а для ещё не выбранных хранилищ `delivery` и `payments` — проверки эквивалентной гарантии.

Повторная публикация остаётся штатной, поэтому consumers обязаны быть идемпотентными по ADR 11. Операции, пишущие Outbox, обязаны быть идемпотентными по idempotency key или `aggregate_version`, что требует хранить ключи или проверять версии в каждом таком сценарии.

Head-of-line blocking сохраняет порядок ценой остановки публикации при сбое одной записи: нужны alert'ы и процедура карантина poison records. В `orders` Forwarder работает как single active instance и масштабируется вертикально; если пропускной способности одного reader станет недостаточно, решение пересматривается. Выбор polling или CDC для не-Go сервисов остаётся открытым.

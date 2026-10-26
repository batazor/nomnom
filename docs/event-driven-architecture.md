# Событийное взаимодействие

Документ описывает надёжную доставку и обработку интеграционных событий между bounded contexts nomnom. Решения зафиксированы в [ADR 4](adr/0004-use-event-driven-architecture-with-apache-kafka.md) (EDA и Apache Kafka), [ADR 13](adr/0013-transactional-outbox-for-integration-event-publishing.md) (Transactional Outbox на стороне publisher) и [ADR 11](adr/0011-transactional-inbox-and-idempotent-event-processing.md) (Transactional Inbox и идемпотентная обработка).

Конкретные топики, контракты и схемы событий, Kafka-клиенты для TypeScript, Rust и Python, схемы Inbox и Outbox, миграции и consumer middleware этим документом не определяются.

## Семантика доставки и обработки

```text
delivery:
  at-least-once

processing:
  idempotent / effectively-once
```

- Transactional Outbox и Kafka обеспечивают [Guaranteed Delivery](https://www.enterpriseintegrationpatterns.com/GuaranteedMessaging.html) с семантикой [At-least-once Delivery](https://www.cloudcomputingpatterns.org/at_least_once_delivery/): событие, зафиксированное в локальной транзакции publisher, не теряется из-за сбоя процесса или недоступности Kafka.
- Одно событие может быть опубликовано и доставлено consumer повторно.
- Порядок гарантируется только внутри одной Kafka partition по выбранному partition key.
- Каждое интеграционное событие имеет стабильный `event_id`.
- Consumer использует Inbox Pattern, чтобы одно логическое событие влияло на его локальное business state не более одного раза.
- Повторная доставка уже обработанного `event_id` не выполняет бизнес-действие повторно.

Система не обеспечивает глобальную distributed exactly-once delivery между микросервисами и целиком не называется exactly-once. Гарантии отдельных компонентов, например exactly-once внутри SQL Pub/Sub Watermill, на межсервисную доставку не переносятся. [Exactly-once Delivery](https://www.cloudcomputingpatterns.org/exactly_once_delivery/) здесь означает желаемое наблюдаемое свойство обработки: оно достигается дедупликацией по `event_id` ([Idempotent Processor](https://www.cloudcomputingpatterns.org/idempotent_processor/)) и локальной транзакцией consumer.

Границы этой гарантии:

| Покрыто | Не покрыто |
| --- | --- |
| Изменения локального хранилища consumer, зафиксированные в одной транзакции с записью Inbox | Вызовы внешних API, платёжных провайдеров, отправка уведомлений и другие эффекты вне локальной транзакции |
| Запись Outbox, созданная в той же транзакции | Повторная доставка в Kafka самого исходящего события: она снова at-least-once и дедуплицируется его consumers |
| Повторы в пределах retention Inbox | Повторы после удаления записи Inbox за пределами поддерживаемого delivery window |
| Один логический consumer (`consumer`) | Согласованность между разными consumers или bounded contexts в один момент времени |

Внешние побочные эффекты получают собственную идемпотентность, например idempotency key внешнего API, производный от стабильного идентификатора операции. Иначе такой эффект выносится за Outbox и выполняется отдельным идемпотентным обработчиком.

## Интеграционное событие

Каждое интеграционное событие несёт метаданные, необходимые для доставки и обработки:

| Поле | Назначение |
| --- | --- |
| `event_id` | Стабильный уникальный идентификатор логического события; основной ключ дедупликации. Передаётся в Kafka header `event_id`. |
| `event_type` | Тип события из ограниченного набора; используется в маршрутизации, логах и метриках. |
| `aggregate_type` | Тип агрегата-источника, например `Order`. |
| `aggregate_id` | Идентификатор агрегата-источника, где применимо. |
| `aggregate_version` | Версия агрегата, где consumer должен распознавать устаревшие или преждевременные события. |
| `occurred_at` | Время возникновения факта; от него считается lag публикации и возраст сообщения. |
| Trace context | W3C `traceparent` и `tracestate` в metadata/Kafka headers по [правилам observability](observability.md#kafka-и-watermill). |
| Correlation и causation | Идентификатор сквозного процесса и `causation_id` — `event_id` входящего события, если исходящее событие создано его обработчиком. |

Kafka message key — идентификатор доменной сущности, чей порядок нужен consumers; для событий заказа — `order_id`.

`event_id` — UUID, который publisher детерминированно вычисляет при создании записи Outbox: UUIDv5 от `(aggregate_type, aggregate_id, aggregate_version, event_type)`; иной способ — только явно в SDD сценария ([ADR 13](adr/0013-transactional-outbox-for-integration-event-publishing.md#стабильность-event_id)). Publisher, повторная публикация и повторная доставка не меняют его. Header `event_id` — то значение, которое читает Inbox consumer; если protobuf envelope события тоже содержит `event_id`, значения совпадают. Формат envelope и размещение остальных метаданных (headers или payload) определяются вместе с контрактами событий.

## Publisher: Transactional Outbox

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

Publisher фиксирует изменение своего состояния и запись Outbox атомарно в одной локальной транзакции. Отдельный Outbox publisher доставляет записи в Kafka после commit и фиксирует факт публикации только после подтверждения Kafka. Факт публикации хранится durable и переживает перезапуск publisher. Outbox предотвращает потерю события между commit локального состояния и публикацией в Kafka.

Запрещены синхронная публикация в Kafka внутри бизнес-транзакции и dual write «commit, затем publish» без Outbox.

Publisher может опубликовать одну запись повторно, например после сбоя между подтверждением Kafka и фиксацией факта публикации. Поэтому Outbox сам по себе даёт at-least-once, а не exactly-once. Повторная публикация не является ошибкой протокола.

Каждый bounded context владеет своим Outbox в своём основном хранилище. Outbox одного контекста не читается и не используется другим.

### Domain events и integration events

Domain Event — факт внутри доменной модели. Integration Event — публичное сообщение для других контекстов с собственным контрактом. Domain event не обязан становиться integration event: application layer решает, что публикует сценарий, и записывает integration event в Outbox в той же транзакции, что и локальное изменение. Kafka не является частью доменной модели.

Кто владеет контрактом, как строятся Integration Event и Event Envelope, кардинальность маппинга и эволюция контрактов описаны в [Domain Events и Integration Events](domain-events.md) ([ADR 25](adr/0025-integration-event-publication.md)).

### Модель записи Outbox

Логическая запись:

```text
event_id
event_type
aggregate_id
aggregate_type
aggregate_version
occurred_at
payload        -- Protobuf-сообщение контракта интеграционного события
metadata       -- trace context, correlation, причинное событие
```

Плюс поля доставки по контракту ADR 37: `topic`, `traceparent` и `created_at` для retention. Состояния публикации (`published_at`, `attempts`) нет: позицию чтения журнала хранит Debezium.

- `event_id` вычисляется в той же транзакции, уникален в хранилище publisher и не меняется при повторной публикации; конфликт уникальности откатывает транзакцию.
- Payload — контракт интеграционного события, а не сериализация агрегата или persistence-модели. Размещение и правила совместимости event-схем в Buf определяются вместе с контрактами событий.
- Trace context операции, создавшей событие, сохраняется в metadata записи и переносится publisher в Kafka headers.

### Стабильность `event_id` при повторе операции

Повтор самой операции после успешного commit — Temporal Activity retry, повторный HTTP- или gRPC-запрос клиента, повторная команда — не должен создавать вторую запись Outbox с новым `event_id` для того же логического факта. Иначе Inbox consumer не распознает дубликат.

Детерминированный `event_id` этого не гарантирует, поэтому обязательна идемпотентность операции: команда, которая изменяет state и пишет Outbox, идемпотентна по idempotency key или по ожидаемой `aggregate_version`. Повтор уже применённой операции не пишет ни state, ни Outbox и возвращает прежний результат.

| Источник повтора | Ключ идемпотентности операции |
| --- | --- |
| Повторный HTTP/gRPC-запрос клиента | Idempotency key команды и сохранённый результат |
| Повторная доставка входящего события | `event_id` входящего события и Inbox |
| Temporal Activity retry в `orders` | Стабильный ключ, производный от Workflow ID и шага процесса, либо ожидаемая `aggregate_version` из workflow |
| Повторная внутренняя команда | Явный idempotency key либо ожидаемая `aggregate_version` |

Детерминированный `event_id` сам по себе не защищает от повторного изменения state: повтор, вычисленный против уже изменённого агрегата, получает новую версию и новый `event_id`. Он делает идентичность события воспроизводимой, а идемпотентность операции не допускает второго изменения.

### Publisher и порядок публикации

- Записи одного partition key публикуются в порядке commit: Debezium читает журнал базы последовательно (ADR 37).
- Упавшая публикация не пропускается: коннектор останавливается (head-of-line blocking), что сопровождается alert'ом.
- Poison outbox record — запись, нарушающая контракт ADR 37, или сообщение, навсегда отклонённое Kafka, — не пропускается молча. После исправления задача коннектора перезапускается.
- Сбой Kafka не останавливает business operation: журнал базы держит события (слот репликации, change stream resume token), они публикуются после восстановления.
- Retention строк Outbox не теряет событий: Debezium читает журнал, а не таблицу.

### Доставка Outbox: Debezium CDC

Решение — [ADR 37](adr/0037-outbox-delivery-via-debezium-cdc.md). Ни один контекст не публикует Outbox сам и не опрашивает таблицу.

```text
Activity / application handler
    |
    v
транзакция хранилища контекста
    +--> state агрегата
    +--> запись Outbox (topic, key, payload, Event Envelope)
commit
    |
    v
журнал базы: PostgreSQL WAL (pgoutput, publication <context>_outbox, только INSERT)
             MongoDB change streams (коллекция outbox)
    |
    v
Kafka Connect: коннектор Debezium <context>-outbox + Outbox Event Router
    |  топик = поле topic, key = aggregate_id, value = payload как есть,
    |  заголовки = поля Event Envelope записи
    v
Kafka
```

- Запись Outbox несёт всё сообщение: `event_id`, `event_type`, `aggregate_type`, `aggregate_id`, `aggregate_version`, `occurred_at`, `topic`, `payload`, `traceparent` (`''` без trace context). Контракт полей — в ADR 37; заголовки совпадают с прежним Event Envelope (`_watermill_message_uuid` = `event_id` для consumers на Watermill), consumers от CDC не зависят.
- Порядок по агрегату сохраняется: журнал упорядочен по commit, ключ Kafka — `aggregate_id`.
- Доставка at-least-once: после рестарта Connect возможны повторы с последнего сохранённого offset; их гасит Inbox ([ADR 11](adr/0011-transactional-inbox-and-idempotent-event-processing.md)).
- Запись, нарушающая контракт, останавливает коннектор (head-of-line): статус `FAILED` в `GET /connectors/<name>/status`, после исправления — restart задачи.
- Записи Outbox хранятся час (`created_at`, у MongoDB — TTL-индекс): журнал удерживает событие независимо от строки, фоновой очистки нет.

### Публикация из Temporal-процесса `orders`

Temporal retries не заменяют Outbox: Temporal — durable orchestration, Kafka — интеграция, Outbox — переход от локальной транзакции к Kafka. Детерминированный workflow-код не пишет в Outbox и не публикует события. Изменение `Order` и запись Outbox выполняются в Activity в одной транзакции PostgreSQL. Повтор Activity и повтор Kafka event — независимые механизмы с независимой идемпотентностью: Activity идемпотентна по [правилу стабильности `event_id`](#стабильность-event_id-при-повторе-операции), Kafka event дедуплицирует Inbox.

### Ошибки publisher

| Сценарий | Состояние после сбоя | Поведение |
| --- | --- | --- |
| Commit не произошёл | State и запись Outbox не сохранены | Событие не существует; операцию можно повторить |
| Commit прошёл, процесс упал до публикации | State и запись Outbox сохранены | Publisher публикует запись после восстановления |
| Kafka приняла сообщение, publisher не зафиксировал успех | Запись остаётся неопубликованной | Повторная публикация с тем же `event_id`; consumer обрабатывает duplicate |
| Consumer выполнил обработку, acknowledgement потерян | Inbox содержит `event_id` | Повторная доставка — no-op у идемпотентного consumer |
| Операция повторена после commit | State и запись Outbox уже сохранены | Идемпотентная операция не пишет ни state, ни Outbox |
| Kafka недоступна | Backlog растёт | Business operation успешна; публикация после восстановления |
| Poison outbox record | Публикация key или topic остановлена | Alert, карантин и ручное восстановление |

## Consumer: Transactional Inbox

### Применимость

Inbox применяется к consumers интеграционных событий, которые изменяют локальное состояние, в контекстах `orders`, `restaurants`, `delivery` и `payments`. Он предназначен для входящих сообщений из системы, внешней относительно bounded context.

Внутренние доменные события, обработка которых происходит внутри одной транзакционной границы приложения, Inbox не используют: их атомарность уже обеспечена этой транзакцией.

### Поток обработки

```text
Kafka event
    |
    v
begin local transaction
    |
    +--> check event_id in inbox
    |
    +--> if already processed:
    |       no-op
    |
    +--> otherwise:
    |       execute application/domain operation
    |       persist business state
    |       persist inbox record
    |
commit
    |
    v
ack message
```

Проверка `event_id`, изменение локального состояния и запись факта обработки выполняются атомарно в одной локальной транзакции, если хранилище это позволяет. Acknowledgement Kafka, то есть commit offset, выполняется только после успешного commit локальной транзакции.

### Модель записи Inbox

Минимальная логическая запись:

```text
event_id
event_type
consumer
processed_at
```

Конкретная реализация может добавлять:

```text
source
aggregate_id
event_version
handler_version
metadata
```

- `event_id` — основной идентификатор дедупликации.
- Для пары `consumer + event_id` существует уникальность. Одно интеграционное событие может независимо обрабатываться несколькими consumers внутри одного bounded context, и каждый из них имеет собственную запись.
- `consumer` — стабильное логическое имя обработчика. Это не идентификатор экземпляра, pod или процесса. Переименование `consumer` равносильно новому обработчику: прежние записи перестают защищать от повторов, поэтому оно выполняется как управляемая миграция.

Запись Inbox принадлежит хранилищу consumer и его bounded context. Соседние контексты её не читают.

## Сквозной pipeline

```text
Service A
────────────────────────

business transaction
    |
    +--> state
    +--> outbox
          |
          v
        Kafka

────────────────────────

Service B

        Kafka
          |
          v
        inbox
          |
          v
business transaction
```

- На стороне publisher Outbox предотвращает потерю события между commit локального состояния и Kafka.
- На стороне receiver Inbox предотвращает повторное применение бизнес-операции при повторной доставке события.

Вместе:

```text
Transactional Outbox
        +
At-least-once delivery
        +
Transactional Inbox
        =
reliable event processing with effectively-once business effects
```

Эта комбинация не является глобальной distributed transaction. Каждый сервис фиксирует только свою локальную транзакцию, а согласованность между контекстами остаётся eventual.

### Consumer публикует новое событие

Если обработчик входящего сообщения одновременно изменяет локальное состояние, фиксирует входящий `event_id` и создаёт новое интеграционное событие, используется одна локальная транзакция:

```text
begin

check inbox

update business state

insert inbox

insert outbox

commit
```

Получается цепочка `Inbox + Business State + Outbox` в одной транзакционной границе bounded context. После commit Outbox publisher отдельно доставляет новое событие в Kafka. Повторная доставка входящего события становится no-op и не создаёт второе исходящее событие.

Это базовый паттерн event-driven взаимодействия `nomnom`.

## Повторная доставка

Первое получение:

```text
event_id not found
    ↓
business operation
    ↓
persist state
    ↓
persist inbox record
    ↓
commit
```

Повторное получение:

```text
event_id already exists
    ↓
skip business operation
    ↓
successful acknowledgement
```

Duplicate не считается бизнес-ошибкой и не уходит в retry или dead-letter. Он учитывается в observability.

Ожидаемый бизнес-исход, при котором событие не меняет состояние, например оно неприменимо к текущему состоянию агрегата, является успешной обработкой: запись Inbox фиксируется. Ошибкой обработки считаются только сбои, после которых транзакция откатывается.

## Ошибки

| Сценарий | Состояние после сбоя | Поведение |
| --- | --- | --- |
| Consumer упал до commit | Business state и запись Inbox не сохранены | Kafka повторно доставляет сообщение, consumer выполняет операцию заново |
| Commit произошёл, consumer упал до acknowledgement | Business state и запись Inbox сохранены, offset не подтверждён | Kafka может повторно доставить событие; Inbox содержит `event_id`, повторная обработка — no-op |
| Business operation завершилась ошибкой | Транзакция откатывается, запись Inbox не фиксирует событие как обработанное | Сообщение может быть обработано повторно согласно retry policy |
| Permanent failure | Сообщение не может быть успешно обработано | Inbox не заменяет стратегию poison messages: сообщение уходит в `<stream>.dlq`, после исправления оператор делает redrive или discard ([Разбор DLQ](dlq.md)) |

Для Go-consumers на Watermill действие по классу ошибки фиксирует [ADR 28](adr/0028-selective-use-of-watermill-cqrs.md); остальные языки следуют той же таблице:

| Класс | Источник | Действие |
| --- | --- | --- |
| Transient (DB, network) | infrastructure / application | retry с backoff и лимитом, затем nack — partition ждёт повторной доставки |
| Deserialization, unknown `event_type`, permanent | infrastructure | `<stream>.dlq` + ack |
| Business error | domain / application | ack; результат — outbox-событие или лог, не retry |
| Duplicate (Inbox) | application | ack, no-op |

Business error не уходит в бесконечный transport retry. Разбор DLQ — [Разбор DLQ](dlq.md): redrive в исходный стрим или discard, по consumer group.

## Конкурентная обработка

Два экземпляра consumer могут одновременно получить или начать обрабатывать один `event_id`, например при rebalance consumer group или повторной публикации Outbox publisher.

Корректность не зависит только от предварительного `SELECT` или `find`. Предварительная проверка допустима как быстрый путь для duplicate, но гарантию даёт уникальность `(consumer, event_id)`, которую обеспечивает сам storage engine. Race condition между `check` и `insert` приводит к одному успешному применению события:

- одна транзакция успешно фиксирует запись Inbox вместе с business state и Outbox;
- другая получает конфликт уникальности, откатывается вместе со всеми своими изменениями и завершает обработку как duplicate с успешным acknowledgement.

Порядок операций внутри транзакции, например вставка записи Inbox до или после бизнес-операции, выбирается реализацией. Требование одно: ни одно изменение business state или Outbox не фиксируется без успешной вставки записи Inbox в той же транзакции.

## Хранилища

| Контекст | Хранилище | Защита Inbox |
| --- | --- | --- |
| `orders` | PostgreSQL | Уникальное ограничение на `(consumer, event_id)` в базе `orders`; запись Inbox в той же транзакции, что и изменения `Order` и Outbox. |
| `restaurants` | MongoDB | Unique index на `{ consumer, event_id }`; multi-document транзакция охватывает запись Inbox, изменённые документы и Outbox. |
| `delivery` | Не выбрано | Эквивалентная атомарная защита обязательна и проверяется при выборе хранилища. |
| `payments` | Не выбрано | Эквивалентная атомарная защита обязательна и проверяется при выборе хранилища. |

Outbox каждого контекста размещается в том же хранилище и фиксируется в той же транзакции, что и его state: таблица SQL Pub/Sub Watermill в PostgreSQL `orders`, коллекция в MongoDB `restaurants`, эквивалентный механизм в хранилищах `delivery` и `payments` после их выбора.

Распределение хранилищ описано в [ADR 8](adr/0008-storage-strategy-postgresql-and-mongodb.md). Конкретные таблицы, коллекции, индексы, миграции и реализации определяются отдельно.

## Ordering

Inbox решает дедупликацию, но не решает нарушение порядка событий. Ordering и idempotency — разные проблемы:

| Проблема | Механизм |
| --- | --- |
| Повторная доставка одного события | Inbox и уникальность `(consumer, event_id)` |
| События одного агрегата пришли не в том порядке | Partition key и при необходимости проверка `aggregate_version` |

Если порядок критичен, Kafka partition key — существующий `aggregate_id` или другой доменный идентификатор. Тогда события одного агрегата попадают в одну partition и читаются в порядке записи в неё.

Порядок может нарушаться и при одном partition key: например, из-за повторной публикации publisher или retry. При необходимости consumer проверяет `aggregate_version` и отклоняет либо откладывает устаревшие или преждевременные события. Отложенное событие не фиксируется в Inbox как обработанное. Устаревшее событие, осознанно отклонённое как неприменимое, является ожидаемым бизнес-исходом и фиксируется.

### Consumer на Watermill CQRS

Application CQRS ≠ Watermill CommandBus; Watermill CQRS = typed event dispatch для Kafka integration ([ADR 28](adr/0028-selective-use-of-watermill-cqrs.md), [CQRS и Watermill](cqrs.md)):

```text
Kafka → Watermill Subscriber → EventProcessor → Watermill handler (infrastructure)
→ deserialize envelope/DTO → map to Command → Application Handler
→ Inbox (insert-first) → UoW → Domain/FSM → Outbox → commit → return nil → ack
```

- Custom marshaler: имя события — `event_type` envelope, payload — Protobuf, `event_id` и trace context — в metadata.
- Топик — stream aggregate контекста-источника (например, `restaurants.v1.restaurant-order-events`), ключ — `aggregate_id`; события внутри стрима маршрутизируются по `event_type`.
- Handlers одного стрима — одна handler group с общим subscriber'ом, чтобы сохранить порядок событий aggregate. Топик-на-тип-события не используется.
- `EventBus.Publish()` не используется: публикация только через Outbox и Debezium (ADR 37).

## Размещение в коде

Сохраняется [DDD-структура внутри vertical slices](adr/0006-ddd-structure-inside-vertical-slices.md):

```text
<slice>/
  domains/
  applications/
  infrastructure/
```

| Каталог | Ответственность |
| --- | --- |
| `domains/` | Бизнес-инварианты. Не знает о Kafka, Inbox, Outbox и `event_id` как механизме доставки. |
| `applications/` | Обработка интеграционного события как application use case: загрузка агрегата, вызов доменного поведения, сохранение результата, создание исходящего события через порт. |
| `infrastructure/` | Kafka consumer, преобразование сообщения в прикладной вход, Inbox persistence, Outbox persistence, транзакция и acknowledgement. |

- Inbox является инфраструктурным механизмом и не попадает в доменную модель.
- Application handler не реализует ручную проверку таблицы или коллекции Inbox.
- Дедупликация оборачивает вызов handler на инфраструктурной транзакционной границе: адаптер открывает локальную транзакцию, обеспечивает запись Inbox, вызывает handler с репозиториями этой транзакции, фиксирует её и только затем подтверждает сообщение.
- Handler определяет атомарную единицу работы сценария, как требует ADR 6. Инфраструктура предоставляет для неё транзакцию и включает в неё записи Inbox и Outbox.
- Обёртка может быть переиспользуемой инфраструктурой запускаемого приложения и собирается в его composition root. Общая библиотека между bounded contexts этим не вводится.
- Допустимый вариант размещения — вызов порта Inbox из handler'а. В `orders` handler сам вызывает `Inbox.Accept(consumer, event_id)` первым шагом внутри своей транзакции, потому что только он упорядочивает действия после commit и повторное уведомление workflow в duplicate-ветке ([design.md change `implement-orders-application-layer`](../openspec/changes/archive/2026-10-25-implement-orders-application-layer/design.md)). Реализация dedupe (`INSERT … ON CONFLICT DO NOTHING`) остаётся в infrastructure, а handler по-прежнему не читает таблицу Inbox напрямую.

## Temporal в `orders`

- Temporal Activity retries не заменяют Inbox Pattern.
- Inbox используется для Kafka integration events; Temporal имеет собственную модель durable execution.
- Событие Kafka, которое запускает workflow или отправляет ему Signal, сначала проходит соответствующую идемпотентную boundary.
- Повторный Kafka event не создаёт второй логический workflow и не применяет Signal повторно, если это нарушает бизнес-семантику.

Стратегия связывания `event_id`, Workflow ID и Signal ID определяется при реализации соответствующего slice. Запуск workflow или отправка Signal — операция вне транзакции PostgreSQL `orders`, поэтому её идемпотентность обеспечивается средствами Temporal, а не только записью Inbox.

## Authorization relationships

SpiceDB не участвует в локальной транзакции business storage или Inbox. Relationship, производный от уже зафиксированного бизнес-факта — например, назначение staff или принятие delivery courier — обновляется отдельным dedicated consumer:

```text
business state + Outbox commit
          ↓
Kafka event
          ↓
dedicated consumer + Inbox → SpiceDB TOUCH/DELETE
```

Такой use case явно документирует допустимое окно eventual consistency. Consumer пишет relationship идемпотентно: upsert использует `TOUCH`, повторный `DELETE` принимает отсутствие relation. Inbox фиксирует successful processing только после успешного side effect либо consumer использует эквивалентную recoverable state machine; crash между SpiceDB write и acknowledgement приводит к безопасному повтору того же idempotent write.

Creation-time и owner relationships, которые должны существовать до публикации resource, не ждут event pipeline: bounded context пишет их синхронно в SpiceDB до local commit. Отказ SpiceDB отменяет commit; отказ local commit после успешного write оставляет orphan relationship для reconciliation. Эта стратегия и lifecycle contract описаны в [документации security](security.md) и [ADR 18](adr/0018-spicedb-for-authorization.md).

Consumer и Temporal Activity авторизуются как собственный `service` subject. Если событие или workflow было инициировано пользователем, исходный subject сохраняется для audit/correlation, но consumer не impersonate пользователя и не повторяет проверку исходной команды.

## Observability

Метрики:

| Сигнал | Назначение |
| --- | --- |
| Processed events | Успешно применённые события. |
| Duplicate events | Повторные доставки, завершённые как no-op. |
| Processing failures | Обработки, завершившиеся откатом транзакции, с низкокардинальным классом ошибки. |
| Processing duration | Гистограмма длительности обработки от получения до commit. |
| Inbox insert conflicts | Конфликты уникальности `(consumer, event_id)`: показатель параллельной обработки одного события. |
| Retry count | Число повторных попыток обработки, где оно известно транспорту или retry policy. |
| Message age | Возраст сообщения на момент обработки, например относительно времени создания события. |

Для Outbox publisher дополнительно:

| Сигнал | Назначение |
| --- | --- |
| Outbox backlog | Число неопубликованных записей. |
| Oldest unpublished age | Возраст самой старой неопубликованной записи: задержка между commit и публикацией. |
| Published events | Записи, подтверждённые Kafka. |
| Publish attempts | Все попытки публикации, включая повторы. |
| Publish failures | Неудачные попытки публикации с низкокардинальным классом ошибки. |
| Publish duration | Гистограмма длительности публикации в Kafka. |
| Publication lag | Гистограмма задержки от `occurred_at` до подтверждения публикации Kafka. |

Для Outbox publisher labels — `event_type` и boundary. Head-of-line blocking и рост возраста самой старой неопубликованной записи сопровождаются alert'ами. Publisher логирует `event_id`, `event_type` и результат публикации.

Watermill-consumers публикуют `nomnom.messaging.*` и `nomnom.inbox.duplicates` ([ADR 28](adr/0028-selective-use-of-watermill-cqrs.md#observability)), consumer lag — на стороне брокера; trace context восстанавливается из headers OpenTelemetry middleware ([ADR 28](adr/0028-selective-use-of-watermill-cqrs.md#observability)).

Допустимые metric labels — низкокардинальные: `consumer`, `event_type`, boundary, результат обработки и класс ошибки. `event_id` и `aggregate_id` не используются как metric labels. Счётчики бизнес-результатов меняются только при фактическом применении события, а не при duplicate, по [общим правилам метрик](observability.md#метрики).

Логи содержат, где применимо:

```text
event_id
event_type
consumer
aggregate_id
```

Trace context продолжает передаваться через metadata интеграционного события. Событие не зависит от существования исходного request span: publisher связывает публикацию с сохранённым в записи Outbox контекстом, а длинные асинхронные цепочки связываются span links. Повторная доставка создаёт отдельную попытку обработки со связью с контекстом сообщения согласно [правилам tracing для Kafka](observability.md#kafka-и-watermill).

## Retention

Записи Outbox хранятся час после вставки: Debezium читает событие из журнала базы, а не из таблицы, поэтому удаление строки не теряет событие. Срок нужен для диагностики; PostgreSQL-контексты удаляют старые строки в транзакции очередной записи Outbox, MongoDB — TTL-индексом ([ADR 37](adr/0037-outbox-delivery-via-debezium-cdc.md)).

Inbox не обязан храниться бесконечно. Retention учитывает максимальное окно, в течение которого Kafka или операционные процедуры могут повторно доставить старое событие: retention топика, сброс или перемотку offsets, восстановление из dead-letter и повторную публикацию из Outbox.

Удаление записей Inbox не должно позволять обычному повтору старого сообщения снова выполнить бизнес-действие в пределах поддерживаемого delivery window. Конкретный срок retention сейчас не задаётся.

## Тестирование

Обязательные fault scenarios:

1. Одно событие доставляется дважды последовательно.
2. Одно событие одновременно обрабатывают два consumer instances.
3. Consumer падает до commit.
4. Consumer падает после commit, но до Kafka acknowledgement.
5. Consumer изменяет state и создаёт новое Outbox event.
6. Повторная доставка не создаёт второе Outbox event.
7. События одного aggregate приходят повторно.
8. При необходимости проверяется обработка out-of-order versions.

Обязательные fault scenarios для Outbox publisher:

1. Откат business transaction не создаёт запись Outbox и событие.
2. Процесс падает после сохранения state и Outbox, но до публикации: publisher публикует событие после перезапуска.
3. Kafka временно недоступна: business operation завершается успешно, backlog растёт и публикуется после восстановления.
4. Publisher отправил сообщение, но не зафиксировал результат: событие публикуется повторно с тем же `event_id`.
5. Consumer получает событие дважды: бизнес-результат применяется один раз.
6. Несколько событий одного aggregate с повторами: порядок по key сохранён.
7. Перезапуск publisher с pending outbox records: публикация продолжается с первой неподтверждённой записи.
8. Повтор команды или Temporal Activity после commit не создаёт второй логический event.
9. Failover publisher'а: события не теряются, дубликаты допустимы.

Сценарии выполняются в kind-based integration environment ([`ops/kind`](../ops/kind/)) с реальными Kafka и хранилищами контекста. Проверяются итоговое business state, число записей Inbox и Outbox, подтверждение offset и метрики duplicate и conflict, а не только вызов handler.

## Открытые вопросы реализации

Общие:

- ~~формат protobuf envelope интеграционного события и место `event_type`, `aggregate_*` и trace context: Kafka headers или payload (`event_id` передаётся в header `event_id`);~~ — решено для consumer `orders` ([ADR 28](adr/0028-selective-use-of-watermill-cqrs.md)): envelope целиком в Kafka headers (`event_id`, `event_type`, `occurred_at` RFC 3339, `aggregate_type`, `aggregate_id`, `aggregate_version`, `traceparent`), payload — Protobuf-сообщение контракта; publishers на других языках используют тот же формат;
- размещение event-схем в Buf и правила их совместимости;
- polling или CDC для Outbox не-Go сервисов, распределение записей между экземплярами publisher с сохранением порядка, срок retention опубликованных записей и процедура ручной повторной публикации;
- процедура карантина и ручного восстановления poison outbox record;
- формат и хранение idempotency key команд и сохранённого результата;
- правила именования `consumer` и процедура его переименования;
- назначение `handler_version` и процедура осознанной повторной обработки событий новой версией handler;
- retention Inbox и способ очистки;
- ~~retry policy, dead-letter topics и ручное восстановление poison messages;~~ — решено [ADR 28](adr/0028-selective-use-of-watermill-cqrs.md#восстановление-из-dlq), runbook — [Разбор DLQ](dlq.md); команды разбора — `orders-admin dlq` и `restaurants-admin dlq`;
- имена метрик, buckets гистограмм и источник времени для message age;
- состав kind-based integration environment для Kafka, PostgreSQL и MongoDB с поддержкой транзакций.

Go (`orders`):

- alert на остановленный коннектор Debezium и лаг слота репликации (ADR 37);
- обёртка Inbox как Watermill middleware или отдельный адаптер вокруг handler; in-memory дедупликация не заменяет durable Inbox;
- передача транзакции PostgreSQL в репозитории и Outbox через unit of work и сборка через Google Wire;
- ключ идемпотентности Activity, производный от Workflow ID и шага процесса;
- подтверждение сообщения и commit offset в `watermill-kafka` строго после commit;
- связывание `event_id`, Workflow ID, Signal ID и политик повторного запуска workflow в Temporal.

TypeScript (`restaurants`):

- выбор Kafka-клиента и ручного commit offsets после транзакции;
- Outbox publisher: polling коллекции Outbox или MongoDB change streams;
- топология MongoDB с поддержкой транзакций, включая локальную и integration-среду;
- передача MongoDB session в репозитории и Outbox через message scope Awilix;
- распознавание конфликта unique index и откат транзакции как duplicate;
- поведение retry при transient transaction errors MongoDB.

Rust (`delivery`):

- выбор хранилища и подтверждение атомарной уникальности `(consumer, event_id)` и атомарной записи state и Outbox;
- Outbox publisher: polling или CDC, библиотека после выбора хранилища;
- выбор Kafka-клиента и явное сохранение offset только после commit;
- передача транзакции через traits репозиториев без зависимости `domains` от драйвера;
- корректный откат при отмене async-задачи посреди транзакции.

Python (`payments`):

- выбор хранилища и подтверждение атомарной уникальности `(consumer, event_id)` и атомарной записи state и Outbox;
- Outbox publisher: polling или CDC, библиотека после выбора хранилища;
- выбор Kafka-клиента и ручной commit offsets после транзакции;
- unit of work и время жизни транзакции на сообщение в Dependency Injector;
- идемпотентность вызовов платёжного провайдера: Inbox не защищает внешнее списание или возврат, нужен idempotency key операции;
- связь дедупликации платёжных webhooks с Inbox: webhooks не являются Kafka integration events и требуют отдельной идемпотентной boundary.

## Источники

- [Idempotent Processor](https://www.cloudcomputingpatterns.org/idempotent_processor/)
- [Exactly-once Delivery](https://www.cloudcomputingpatterns.org/exactly_once_delivery/)
- [Guaranteed Delivery](https://www.enterpriseintegrationpatterns.com/GuaranteedMessaging.html)
- [At-least-once Delivery](https://www.cloudcomputingpatterns.org/at_least_once_delivery/)
- [Watermill SQL Pub/Sub](https://watermill.io/pubsubs/sql/)
- [Debezium Outbox Event Router](https://debezium.io/documentation/reference/stable/transformations/outbox-event-router.html)
- [Debezium Outbox Event Router](https://debezium.io/documentation/reference/stable/transformations/outbox-event-router.html)

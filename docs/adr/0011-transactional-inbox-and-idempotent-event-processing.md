# 11. Transactional Inbox and Idempotent Event Processing

Date: 2026-10-24

## Status

Accepted

Дополняет [4. Использовать Event-Driven Architecture и Apache Kafka](0004-use-event-driven-architecture-with-apache-kafka.md)

Связан с [13. Transactional Outbox for Integration Event Publishing](0013-transactional-outbox-for-integration-event-publishing.md)

## Context

[ADR 4](0004-use-event-driven-architecture-with-apache-kafka.md) выбирает интеграционные события и Apache Kafka для асинхронного взаимодействия bounded contexts. Для этого взаимодействия уже приняты:

- Transactional Outbox на стороне publisher ([ADR 13](0013-transactional-outbox-for-integration-event-publishing.md));
- Kafka как транспорт;
- at-least-once delivery;
- обязательная идемпотентность consumers.

Outbox предотвращает потерю события между commit локального состояния publisher и публикацией в Kafka, но не предотвращает повторы. Relay может опубликовать запись Outbox повторно, Kafka может повторно доставить сообщение, consumer может упасть после commit локальной транзакции, но до commit offset. Поэтому одно логическое интеграционное событие может прийти к consumer несколько раз.

Без общего механизма каждый consumer решал бы дедупликацию по-своему: предварительным чтением, сравнением состояния или не решал бы её вовсе. Предварительная проверка без ограничения хранилища не защищает от параллельной обработки одного события двумя экземплярами consumer. Глобальной distributed transaction между Kafka, хранилищами разных контекстов и внешними системами в архитектуре нет и не вводится ([ADR 8](0008-storage-strategy-postgresql-and-mongodb.md)).

На этом шаге фиксируется только архитектурное решение. Inbox storage, миграции, индексы, consumer middleware и тесты не создаются.

## Decision

### Семантика доставки и обработки

```text
delivery:
  at-least-once

processing:
  idempotent / effectively-once
```

Kafka и Outbox обеспечивают доставку с семантикой at-least-once: одно событие может быть доставлено consumer повторно. Каждое интеграционное событие имеет стабильный `event_id`, который назначается при создании записи Outbox и не меняется при повторной публикации или повторной доставке.

Consumer, изменяющий своё локальное состояние в ответ на интеграционное событие, использует Transactional Inbox: одно логическое событие влияет на локальное business state этого consumer не более одного раза. Повторная доставка уже обработанного `event_id` не выполняет бизнес-действие повторно и завершается успешным acknowledgement.

Система не утверждает глобальную distributed exactly-once delivery между микросервисами. [Exactly-once Delivery](https://www.cloudcomputingpatterns.org/exactly_once_delivery/) используется только как название наблюдаемого свойства обработки, которое достигается дедупликацией в [Idempotent Processor](https://www.cloudcomputingpatterns.org/idempotent_processor/) и локальной транзакцией. Гарантия ограничена:

- одним consumer и его локальным хранилищем;
- изменениями, которые фиксируются в той же локальной транзакции, что и запись Inbox;
- окном retention записей Inbox.

Вызовы внешних систем, отправка уведомлений и другие побочные эффекты вне локальной транзакции этой гарантией не покрываются. Для них нужна собственная идемпотентность, например idempotency key внешнего API, либо перенос эффекта за Outbox.

### Inbox Pattern

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

Проверка `event_id`, изменение локального состояния и запись факта обработки выполняются атомарно в одной локальной транзакции. Acknowledgement в Kafka, то есть commit offset, выполняется только после успешного commit.

Если обработчик входящего события также публикует новое интеграционное событие, Inbox, business state и Outbox фиксируются в одной транзакционной границе bounded context:

```text
begin
  check inbox
  update business state
  insert inbox
  insert outbox
commit
```

После commit Outbox publisher отдельно доставляет новое событие в Kafka. Цепочка `Inbox + Business State + Outbox` является базовым паттерном event-driven взаимодействия `nomnom`:

```text
Transactional Outbox
        +
At-least-once delivery
        +
Transactional Inbox
        =
reliable event processing with effectively-once business effects
```

Эта комбинация не является глобальной distributed transaction: каждый участник фиксирует только свою локальную транзакцию.

### Область применения

Inbox применяется к consumers интеграционных событий в `orders`, `restaurants`, `delivery` и `payments`. Он предназначен для входящих сообщений из системы, внешней относительно bounded context.

Внутренние доменные события, которые обрабатываются внутри одной транзакционной границы приложения, Inbox не используют.

### Минимальная модель

Логическая запись Inbox:

```text
event_id
event_type
consumer
processed_at
```

Конкретная реализация может добавлять `source`, `aggregate_id`, `event_version`, `handler_version` и `metadata`.

`event_id` является основным идентификатором дедупликации. Для пары `consumer + event_id` хранилище обеспечивает уникальность. Поэтому одно интеграционное событие может независимо обрабатываться несколькими consumers внутри одного bounded context. `consumer` — стабильное логическое имя обработчика, а не идентификатор экземпляра процесса.

### Хранилища и конкурентная обработка

Корректность не зависит только от предварительного `SELECT` или `find`. Уникальность записи Inbox обеспечивается самим storage engine:

- PostgreSQL-сервисы используют уникальное ограничение на `(consumer, event_id)`;
- MongoDB-сервисы используют соответствующий unique index и транзакцию, охватывающую запись Inbox и изменённые документы;
- для других storage engines должна существовать эквивалентная атомарная защита.

Если два экземпляра consumer одновременно обрабатывают один `event_id`, конфликт уникальности приводит к одному успешному применению события. Проигравшая транзакция откатывается вместе со своими изменениями business state и Outbox, а сообщение считается duplicate. Конкретные схемы, индексы и реализации определяются отдельно.

### Ошибки

- Consumer упал до commit: business state и запись Inbox не сохранены. Kafka повторно доставляет сообщение, и операция выполняется заново.
- Commit произошёл, consumer упал до acknowledgement: Kafka может повторно доставить событие. Inbox уже содержит `event_id`, поэтому повторная обработка превращается в no-op.
- Business error (ожидаемый отказ домена или application handler): транзакция откатывается вместе с записью Inbox, сообщение подтверждается и не ретраится. Если Kafka всё же доставит его снова, отказ повторится без изменения состояния. Исход, который должен остаться фактом, use case возвращает как успешный результат и фиксирует вместе с Inbox и Outbox: откат отменил бы и его.
- Transient failure (БД, сеть): транзакция откатывается, сообщение повторяется с backoff; после исчерпания повторов partition ждёт зависимость.
- Permanent failure: сообщение уходит в `<stream>.dlq` и подтверждается, запись Inbox не фиксируется.

Классы ошибок и действия consumer определены в [ADR 28](0028-selective-use-of-watermill-cqrs.md#ошибки), разбор DLQ — в [runbook](../dlq.md).

Duplicate не является бизнес-ошибкой и учитывается в observability.

### VSA и DDD

Сохраняется структура из [ADR 6](0006-ddd-structure-inside-vertical-slices.md):

- `domains` — бизнес-инварианты;
- `applications` — обработка интеграционного события как application use case через Command слайса;
- `infrastructure` — Kafka consumer, преобразование event payload в Command, Inbox persistence, Outbox persistence и acknowledgement.

Integration event не является отдельным типом прикладной модели: payload — Transport DTO consumer'а, который преобразуется в Command, например `MarkRestaurantAcceptedCommand`, и передаётся обычному command handler ([ADR 6](0006-ddd-structure-inside-vertical-slices.md#входы-в-application)).

Inbox является инфраструктурным механизмом и не попадает в доменную модель. Application handler не реализует ручную проверку таблицы или коллекции Inbox. Дедупликация оборачивает вызов handler на инфраструктурной транзакционной границе. Handler по-прежнему определяет атомарную единицу работы сценария, как требует ADR 6, а инфраструктурный адаптер предоставляет для неё локальную транзакцию и включает в неё записи Inbox и Outbox.

### Ordering

Inbox решает дедупликацию, но не решает нарушение порядка событий. Ordering и idempotency являются разными проблемами.

Если порядок критичен, Kafka partition key — `aggregate_id` или другой доменный идентификатор. При необходимости consumer дополнительно проверяет `aggregate_version` и отклоняет либо откладывает устаревшие или преждевременные события.

### Temporal в `orders`

Temporal Activity retries не заменяют Inbox Pattern. Inbox используется для Kafka integration events, а Temporal имеет собственную модель durable execution. Событие Kafka, которое запускает workflow или отправляет ему Signal, сначала проходит соответствующую идемпотентную boundary. Повторный Kafka event не создаёт второй логический workflow и не применяет Signal повторно, если это нарушает бизнес-семантику. Связывание в `orders` ([ADR 5 orders](../../boundaries/orders/docs/adr/0005-order-fulfillment-orchestration.md)):

- Workflow ID — `order:<order_id>`: один workflow на `Order`, повторный старт адресует существующий (`USE_EXISTING`).
- Старт — только consumer `orders.startfulfillment` собственного `OrderPlaced`: Inbox и чтение `Order` в одной UoW, после commit — `SignalWithStart(order:<order_id>, OrderPlaced)`. Дубликат Inbox тоже выполняет `SignalWithStart` (лечит crash между commit Inbox и стартом); для завершённого `Order` старт не выполняется; ошибка Temporal оставляет сообщение неподтверждённым.
- Имя Signal совпадает с именем Integration Event, в Signal передаётся `event_id`; workflow хранит обработанные `event_id` и превращает повтор Signal в no-op. Отдельного Signal ID нет.

### Retention

Inbox не обязан храниться бесконечно. Retention учитывает максимальное окно, в течение которого Kafka или операционные процедуры могут повторно доставить старое событие. Удаление записей Inbox не должно позволять обычному повтору старого сообщения повторно выполнить бизнес-действие в пределах поддерживаемого delivery window. Конкретный срок сейчас не задаётся.

Подробности, observability и обязательные fault scenarios описаны в [документации событийного взаимодействия](../event-driven-architecture.md).

## Consequences

Повторная доставка становится штатным и безопасным сценарием для consumers всех контекстов: Kafka, Outbox relay и consumer могут повторять работу без повторных бизнес-эффектов в локальном состоянии. Связка Inbox, business state и Outbox даёт единый паттерн для цепочек событий между контекстами.

Каждый consumer, изменяющий состояние, получает дополнительную запись в своей транзакции и уникальное ограничение в хранилище. Нужны retention и очистка Inbox, а также тесты на повторную и параллельную доставку. Хранилище consumer должно поддерживать атомарную запись Inbox и business state; для MongoDB это требует транзакций, а для ещё не выбранных хранилищ `delivery` и `payments` — проверки эквивалентной гарантии.

Гарантия effectively-once ограничена локальной транзакцией consumer. Внешние побочные эффекты и порядок событий требуют отдельных механизмов и не решаются этим ADR, poison messages разбираются по [ADR 28](0028-selective-use-of-watermill-cqrs.md#ошибки); связывание с Temporal в `orders` описано выше и в ADR 5 orders.

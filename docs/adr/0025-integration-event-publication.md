# 25. Integration Event Publication

Date: 2026-10-25

## Status

Accepted

Дополняет [13. Transactional Outbox for Integration Event Publishing](0013-transactional-outbox-for-integration-event-publishing.md)

Связан с [6. DDD Structure Inside Vertical Slices](0006-ddd-structure-inside-vertical-slices.md), [7. gRPC Contracts and Buf Schema Registry](0007-grpc-contracts-and-buf-schema-registry.md), [11. Transactional Inbox and Idempotent Event Processing](0011-transactional-inbox-and-idempotent-event-processing.md), [20. Unit of Work for Local Transactions](0020-unit-of-work-for-local-transactions.md), [21. Pure FSM Transitions](0021-pure-fsm-transitions.md) и [24. Ubiquitous Language for Domain Modeling](0024-ubiquitous-language-for-domain-modeling.md)

## Context

[ADR 13](0013-transactional-outbox-for-integration-event-publishing.md#domain-events-и-integration-events) разделяет Domain Event и Integration Event и требует записывать integration events в Outbox в одной транзакции со state. [ADR 21](0021-pure-fsm-transitions.md#domain-methods-и-events) фиксирует, что aggregate накапливает domain events в памяти и отдаёт их через механизм наподобие `PullEvents()`. [ADR 6](0006-ddd-structure-inside-vertical-slices.md#mapping) говорит, что application передаёт в порт «доменное событие или Application DTO», а infrastructure преобразует его в Protobuf.

Эти решения не отвечают на несколько вопросов, и без ответа каждый slice решит их по-своему:

- кто владеет контрактом integration event: domain, application или infrastructure;
- какой слой решает, публиковать ли факт, и какой строит payload и envelope;
- когда допустимо отдельное application-представление исходящего события, а когда это лишний маппинг;
- обязана ли структура integration event повторять domain event;
- сколько данных несёт событие;
- как эволюционирует контракт;
- можно ли обрабатывать domain events in-process другими slices.

На этом шаге фиксируются документация и решение. Protobuf-схемы, mappers, порты и adapters не создаются. Подробности и примеры — в [документации domain и integration events](../domain-events.md) и [application models](../application-models.md).

## Decision

### Domain events не публикуются напрямую

Domain Event — внутренний бизнес-факт bounded context: domain types, без side effects, без Kafka, Protobuf, Watermill и DTO. Он не сериализуется в Kafka и не сохраняется в Outbox как есть. Наружу выходит только Integration Event, который строит infrastructure.

### Ответственность слоёв

```text
domains        → бизнес-факт (Domain Event)
applications   → решение: публиковать ли, какой факт, какие данные разрешены наружу
infrastructure → маппинг в Integration Event, Event Envelope, сериализация, Outbox, Kafka
```

- Domain не знает topic, Protobuf package, версию схемы, headers и способ сериализации.
- Application не сериализует и не зависит от transport. По умолчанию handler забирает domain events из aggregate и передаёт в порт Outbox те, которые сценарий публикует, вместе со ссылкой на aggregate (`aggregate_type`, `aggregate_id`, `aggregate_version`).
- Application DTO исходящего события вводится только тогда, когда payload требует данных сверх domain event, например enrichment из read model, загруженной в той же UoW. В остальных случаях отдельная модель — лишний маппинг и не создаётся ([ADR 6](0006-ddd-structure-inside-vertical-slices.md#типы-моделей)).
- Infrastructure маппит вход порта в Integration Event, заполняет Event Envelope, сериализует сообщение и пишет Outbox record в текущую транзакцию.

### Контракт — Published Language контекста

Integration Event — Protobuf-контракт в BSR-модуле контекста-publisher (`buf.build/nomnom/orders`, package `nomnom.orders.v1` по [ADR 7](0007-grpc-contracts-and-buf-schema-registry.md)), например `nomnom.orders.v1.OrderCancelled`. Он и есть Transport DTO исходящего события; отдельный термин для него не вводится.

Владелец контракта — bounded context: состав событий и полей является Published Language ([ADR 24](0024-ubiquitous-language-for-domain-modeling.md)) и меняется через review владельца, как публичный API. Infrastructure владеет реализацией маппинга, но не принимает решений о составе контракта.

### Кардинальность и форма маппинга

Маппинг не обязан быть 1:1. Один domain event может дать несколько integration events, несколько domain events — одно integration event, domain event может не публиковаться вовсе. Структуры domain event и integration event не обязаны совпадать. Кардинальность решается только в mapper и фиксируется в SDD сценария.

Mapper — чистый код: он не обращается к хранилищам и внешним системам, не загружает aggregate и не добавляет данные, которых нет во входе порта. Mapper размещается в `infrastructure/` рядом с messaging adapter slice (`infrastructure/messaging/` или эквивалент); глобальный mapper layer и reflection-based automapping не используются.

### Event Envelope

Infrastructure заполняет Event Envelope с логическим составом из ADR 13:

```text
event_id, event_type, occurred_at,
aggregate_type, aggregate_id, aggregate_version,
payload,
metadata: trace context, correlation_id, causation_id
```

`occurred_at` берётся из domain event (время факта, переданное значением в domain method), а не из часов publisher. Envelope передаётся в Kafka headers ([ADR 28](0028-selective-use-of-watermill-cqrs.md)).

### Payload и Schema Registry

Outbox хранит payload — байты Protobuf контракта. Value записи стрима Integration Events — тот же payload в wire format Schema Registry: magic byte `0`, id схемы (4 байта big-endian), индексы сообщения в файле и байты Protobuf. Префикс дописывает SMT `SchemaFraming` коннектора Outbox ([ADR 37](0037-outbox-delivery-via-debezium-cdc.md)) по header `event_type`, поэтому publishers на любом языке префикс не пишут и Registry при записи в Outbox не нужен. Change Notifications ([ADR 36](0036-graphql-subscriptions-via-cosmo-streams.md)) — не Integration Events: их value — JSON без префикса.

- Registry — Confluent Schema Registry (в kind — Deployment `schema-registry` рядом с брокером Strimzi, схемы в topic `_schemas`). Интеграция BSR с Confluent Schema Registry доступна только в Enterprise-плане Buf, а регистрация схем там оплачивается отдельно. Код рассчитан на API Confluent (`?format=serialized`, `/schemas/ids/{id}/versions`): другой Registry с тем же API меняет только URL.
- Subject Integration Event — полное имя Protobuf-сообщения, равное `event_type` (record name strategy): в одном стриме несколько типов событий. Импортированный файл модуля регистрируется под subject = путь файла и указывается в `references`.
- Схемы регистрирует только шаг деплоя `ops/schema-registration` из buf-модулей контекстов ([ADR 7](0007-grpc-contracts-and-buf-schema-registry.md)); producers, SMT и consumers Registry только читают.
- Глобальный режим совместимости Registry — `FULL_TRANSITIVE`: несовместимая версия (смена типа поля, удаление сообщения) отклоняется и останавливает деплой. Удаление поля и перестановку номеров ловит `buf breaking` в PR.
- Consumer сверяет subjects id схемы записи с `event_type`. Value вне wire format, неизвестный id и схема другого `event_type` — `deserialization`, запись уходит в `<stream>.dlq`; недоступный Registry — transient, partition ждёт.

### Стабильный `event_id`

По умолчанию `event_id` детерминирован: infrastructure вычисляет UUIDv5 от `(aggregate_type, aggregate_id, aggregate_version, event_type)` при создании Outbox record. Иной способ — только явно, в SDD сценария, например когда одна UoW публикует несколько событий одного типа для одного aggregate. Детерминированный `event_id` не заменяет идемпотентность операции по idempotency key или ожидаемой `aggregate_version`. Правило внесено в [ADR 13](0013-transactional-outbox-for-integration-event-publishing.md#стабильность-event_id) вместо прежнего UUID, генерируемого при записи.

### State-carrying по умолчанию

Integration event по умолчанию state-carrying: несёт факт и данные, достаточные consumers для реакции без обратного вызова publisher. Payload минимизируется: без PII и внутренних полей сверх реальной потребности consumers. Состав payload — часть контракта и фиксируется в SDD.

### Outbox и publisher

В Outbox записывается transport-ready событие: сериализованный Integration Event вместе с Event Envelope. Business state и Outbox record фиксируются в одной Unit of Work ([ADR 20](0020-unit-of-work-for-local-transactions.md)). Publisher читает готовые записи, публикует их и фиксирует результат; он не загружает aggregate, не видит domain events и не выполняет domain mapping.

### Domain events внутри контекста

In-process обработка domain event другим slice допустима только синхронно, в той же UoW и только для изменения локального state своего хранилища, без внешних вызовов. Реакция, выходящая за транзакцию, в том числе реакция контекста на собственный факт (например, старт fulfillment workflow после оформления заказа), проходит через Integration Event и Inbox. In-process dispatcher, который выполняет обработчики domain events после commit, не вводится.

Явные действия самого use case после commit через порт (Signal или start Temporal workflow по [ADR 6](0006-ddd-structure-inside-vertical-slices.md#входы-в-application) и ADR 20) обработкой domain event не являются; правило их не меняет.

### Входящие события

```text
Kafka → Transport DTO (proto) → infrastructure mapper → Command → handler → domain
```

Обработка проходит через Inbox ([ADR 11](0011-transactional-inbox-and-idempotent-event-processing.md)). Transport DTO и Integration Event соседа в domain не передаются.

### Versioning и эволюция

Версионируется контракт integration event, а не domain event: domain event меняется вместе с моделью как внутренний код.

- Изменения additive-first: новые поля optional, существующие поля не меняют смысл, номера полей не переиспользуются; проверка — `buf breaking` в PR, затем `FULL_TRANSITIVE` Registry при регистрации. Схема нового события или поля регистрируется до деплоя publisher'а: запись без subject останавливает коннектор.
- Breaking change — новая major-версия package (`nomnom.orders.v2.*`). На период миграции consumers publisher выполняет dual publish v1 и v2, а срок вывода v1 фиксируется в SDD изменения.
- Mapper отвечает за построение всех поддерживаемых версий. Каждая версия — отдельный Integration Event со своим `event_type` и `event_id`.

## Consequences

Domain model остаётся независимой от transport и формата контрактов: рефакторинг domain events не ломает consumers, пока mapper сохраняет контракт. Публичный контракт получает явного владельца и review, а утечка внутренних полей наружу становится нарушением, которое видно на review.

Цена — явный mapper и тесты маппинга для каждого публикуемого события, а при breaking change — временная поддержка двух версий и dual publish. State-carrying payload увеличивает размер сообщений и требует осознанной минимизации данных.

Запрет асинхронных in-process обработчиков domain events переводит такие реакции на Outbox → Kafka → Inbox: появляется задержка и инфраструктурная цепочка даже внутри одного контекста, зато реакция переживает сбой процесса и дедуплицируется по `event_id`.

Schema Registry становится зависимостью доставки: пока он недоступен, коннекторы Outbox в `FAILED`, а consumers ждут. Порядок деплоя — регистрация схем до publisher'а — требование к pipeline окружения.

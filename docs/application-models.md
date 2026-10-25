# Application Models

Документ описывает модели, с которыми работает use case, и место событий среди них. Базовые правила зафиксированы в [ADR 6: DDD Structure Inside Vertical Slices](adr/0006-ddd-structure-inside-vertical-slices.md#типы-моделей), правила публикации событий — в [ADR 25: Integration Event Publication](adr/0025-integration-event-publication.md). Путь событий подробно описан в [Domain Events и Integration Events](domain-events.md).

## Виды моделей

```text
Transport DTO → Command / Query → Domain Model
Domain Model / Read Model → Application DTO → Transport DTO
Domain Model → Domain Event → [Application DTO] → Integration Event (Transport DTO)
```

| Модель | Назначение | Размещение | Не зависит от |
| --- | --- | --- | --- |
| Transport DTO | Форма данных adapter'а: Protobuf, Kafka message, HTTP/webhook payload, Temporal Activity input/output. Integration Event — Transport DTO событий. | `infrastructure/` adapter'а-владельца | — |
| Command | Намерение изменить state: `PlaceOrder`, `CancelOrder`. | `<slice>/applications/<usecase>/` | transport, ORM; без бизнес-логики |
| Query | Чтение без изменения state: `GetOrder`, `ListCustomerOrders`. | `<slice>/applications/<usecase>/` | transport, ORM |
| Application DTO | Вход/выход use case, когда domain object не подходит наружу; read model query. | `<slice>/applications/<usecase>/` | transport; без поведения |
| Domain Model | Aggregates, entities, value objects, Domain Events, доменные ошибки. | `<slice>/domains/<domain>/` | всего перечисленного выше |

Integration Event не является отдельным видом прикладной модели: на выходе это Transport DTO, который строит infrastructure, на входе — Transport DTO, который consumer переводит в Command ([ADR 11](adr/0011-transactional-inbox-and-idempotent-event-processing.md)).

## Исходящие события

### По умолчанию: Domain Event в порт Outbox

Handler забирает Domain Events из aggregate через `PullEvents()` и передаёт в порт Outbox те, которые сценарий публикует, вместе со ссылкой на aggregate (`aggregate_type`, `aggregate_id`, `aggregate_version`). Отдельная application-модель не создаётся: infrastructure mapper строит Integration Event прямо из Domain Event.

Решение application состоит из трёх частей:

1. **Публиковать ли** — какие из забранных Domain Events уходят в порт.
2. **Какой факт** — какой Domain Event (или их набор) представляет публикуемый факт сценария.
3. **Какие данные разрешены наружу** — в пределах контракта: mapper не может добавить данные, которых application не передала.

### Application DTO исходящего события

Application DTO исходящего события вводится только тогда, когда payload контракта требует данных сверх Domain Event, например:

- enrichment из read model своего контекста, загруженной в той же UoW;
- данные из Command, которые не являются частью доменного факта, но нужны consumers.

Правила:

- DTO собирается в `applications/<usecase>/` внутри UoW из domain types, read model и Command; без transport-типов и Protobuf.
- Данные других контекстов в DTO попадают только через вход use case: внешние вызовы внутри UoW запрещены ([UoW](unit-of-work.md#только-локальное-хранилище)).
- Если дополнительные данные нужны всем сценариям, публикующим этот факт, их место — в Domain Event, а не в DTO каждого handler'а.
- Имя следует факту и соглашению ADR 6, например `OrderCancelledDTO`; новый термин словаря для таких моделей не вводится.

Создавать DTO «на всякий случай», когда он копирует поля Domain Event, запрещено: это лишний маппинг без новых данных ([ADR 6](adr/0006-ddd-structure-inside-vertical-slices.md#типы-моделей)).

### Что application не делает

- не создаёт Protobuf-сообщения и не импортирует generated code;
- не выбирает topic, partition key, версию схемы и headers;
- не заполняет Event Envelope и не назначает `event_id`;
- не сериализует payload и не обращается к Kafka.

## Входящие события

```text
Kafka → Integration Event соседа (Transport DTO) → infrastructure mapper → Command → handler
```

Consumer в `infrastructure/` переводит Integration Event в Command своего контекста и вызывает handler внутри Inbox ([ADR 11](adr/0011-transactional-inbox-and-idempotent-event-processing.md)). Handler работает так же, как для gRPC или Temporal Activity: один use case — одна пара Command/handler для всех entrypoints. Integration Event соседа не попадает в domain и не используется как Domain Event consumer'а.

## Mapping

| Направление | Где |
| --- | --- |
| Transport DTO ↔ Command / Query / Application DTO | `infrastructure/`, adapter-владелец Transport DTO |
| Application ↔ domain | `applications/`, только где типы различаются |
| Persistence ↔ domain | реализация repository в `infrastructure/` |
| Domain Event / Application DTO → Integration Event | mapper в `infrastructure/messaging/` slice ([domain events](domain-events.md#маппинг)) |
| Integration Event соседа → Command | mapper consumer'а в `infrastructure/messaging/` slice |

Mapping пишется явным кодом; глобальный mapper layer и reflection-based automapping не используются.

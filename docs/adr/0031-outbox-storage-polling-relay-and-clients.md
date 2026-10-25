# 31. Outbox Storage, Polling Relay and Client Libraries for payments, delivery and restaurants

Date: 2026-10-25

## Status

Accepted; доставка Outbox (polling relay и relay-библиотеки) заменена [37. Доставка Outbox через CDC на Debezium](0037-outbox-delivery-via-debezium-cdc.md)

Дополняет [8. Storage Strategy: PostgreSQL and MongoDB](0008-storage-strategy-postgresql-and-mongodb.md) и [13. Transactional Outbox for Integration Event Publishing](0013-transactional-outbox-for-integration-event-publishing.md)

Связан с [11. Transactional Inbox and Idempotent Event Processing](0011-transactional-inbox-and-idempotent-event-processing.md), [17. Cloud Native Application Runtime](0017-cloud-native-application-runtime.md), [20. Unit of Work for Local Transactions](0020-unit-of-work-for-local-transactions.md), [25. Integration Event Publication](0025-integration-event-publication.md)

## Context

ADR 13 требует Transactional Outbox во всех publishers, но для `restaurants` (TypeScript), `delivery` (Rust) и `payments` (Python) откладывает выбор библиотек до выбора хранилищ и оставляет открытым вопрос «polling или CDC». ADR 8 выбирает MongoDB для `restaurants` и не выбирает хранилища `payments` и `delivery`. Все три контекста пока держат state в памяти процесса, поэтому их события не могут быть опубликованы надёжно.

## Decision

### Хранилища

| Bounded context | Основное хранилище | Атомарность state + Outbox |
| --- | --- | --- |
| `payments` | PostgreSQL | одна транзакция PostgreSQL |
| `delivery` | PostgreSQL | одна транзакция PostgreSQL |
| `restaurants` | MongoDB (replica set) | multi-document транзакция MongoDB |

PostgreSQL выбран для `payments` и `delivery` по тем же причинам, что для `orders`: связанные записи (Payment и его операции, Delivery и назначения курьеров) меняются в одной транзакции, уникальность idempotency key, `order_id` и `(consumer, event_id)` Inbox обеспечивается ограничениями, конкурентные изменения отсекаются optimistic concurrency по `version`. MongoDB для `restaurants` требует replica set: транзакции на standalone-сервере недоступны. Каждый контекст владеет отдельной базой и учётными данными.

### Polling relay, не CDC

Outbox читает **polling relay** — отдельный процесс `<context>-relay` того же образа, по аналогии с `orders-forwarder`. CDC (Debezium, MongoDB change streams) не используется: он добавляет Kafka Connect, logical replication или resume tokens как эксплуатируемую инфраструктуру, а `orders` уже использует polling (`watermill-sql`). Решение пересматривается, если задержка или нагрузка polling станут неприемлемыми.

Правила relay:

- **Single active instance.** PostgreSQL — session-level `pg_try_advisory_lock` с константным ключом контекста; MongoDB — lease-документ в коллекции `outbox_leases` с владельцем и сроком, продлеваемым каждый цикл. Без lease relay не читает Outbox; replicas служат failover.
- **Выборка.** Каждый цикл читает пачку неопубликованных записей (`published_at` пуст) в порядке вставки (`seq` — `BIGSERIAL` в PostgreSQL; монотонный `seq` из счётчика контекста в MongoDB, выделяемый в той же транзакции). Offset не хранится: запись, зафиксированная позже записи с большим `seq`, будет прочитана следующим циклом. Записи одного aggregate упорядочены, потому что optimistic concurrency сериализует их транзакции.
- **Публикация и подтверждение.** Запись публикуется в Kafka idempotent producer'ом с `acks=all`; `published_at` выставляется только после подтверждения брокера. Повторная публикация после сбоя штатна (ADR 13).
- **Head-of-line.** Первая неудачная запись останавливает пачку; следующий цикл начинает с неё же. Записи после неё в этом цикле не публикуются, `attempts` и `last_error` записи обновляются.
- **Retention.** Опубликованные записи старше `OUTBOX__RETENTION` (по умолчанию 7 дней) удаляет relay; неопубликованные не удаляются.
- **Observability.** Метрики ADR 13 через OpenTelemetry: backlog, возраст старейшей неопубликованной записи, попытки и ошибки, длительность публикации, lag `occurred_at` → подтверждение; labels — `event_type` и `boundary`.

### Запись Outbox и Event Envelope

Логическая запись ADR 13: `event_id`, `event_type`, `aggregate_type`, `aggregate_id`, `aggregate_version`, `occurred_at`, `topic`, `payload` (Protobuf контракта), `metadata` (`traceparent`, `causation_id`), плюс `seq`, `published_at`, `attempts`, `last_error`.

- `event_id` — UUIDv5 в namespace `8f1d3c2a-5b7e-4a90-9c61-2e4f7b8d0a13` от `"{aggregate_type}:{aggregate_id}:{aggregate_version}:{event_type}"`, где `event_type` — полное имя Protobuf-сообщения. Уникальный индекс по `event_id`; конфликт откатывает транзакцию. Если одна транзакция создаёт для aggregate несколько событий одного типа, use case фиксирует иной способ в SDD.
- Kafka message key — `aggregate_id`. Kafka headers — `event_id`, `event_type`, `occurred_at` (RFC 3339), `aggregate_type`, `aggregate_id`, `aggregate_version`, `traceparent`; формат совпадает с `orders` (`pkg/messaging/envelope.go`), чтобы consumers не зависели от языка publisher.
- Mapper Domain Event → Integration Event находится в `infrastructure` слайса. Domain Event без контракта не попадает в Outbox: mapper отбирает публикуемые типы явно, а не по умолчанию.

### Topics

| Context | Topic | Контракт |
| --- | --- | --- |
| `payments` | `payments.v1.payment-events` | `nomnom/payments/v1/events.proto` |
| `delivery` | `delivery.v1.delivery-events` | `nomnom/delivery/v1/events.proto` |
| `restaurants` | `restaurants.v1.restaurant-order-events` | `nomnom/restaurants/v1/events.proto` |

### Библиотеки

| Context | Хранилище | Миграции | Kafka |
| --- | --- | --- | --- |
| `payments` | `psycopg` 3 (синхронный, как handlers) с `psycopg_pool` | версионированные SQL-файлы, применяемые процессом `payments-migrate` | `confluent-kafka` |
| `delivery` | `sqlx` (PostgreSQL, tokio) | `sqlx::migrate!`, процесс `delivery-migrate` | `rdkafka` |
| `restaurants` | официальный driver `mongodb` | идемпотентное создание коллекций, validators и индексов процессом `restaurants-migrate` | `@confluentinc/kafka-javascript` |

Все три Kafka-клиента построены на librdkafka и дают одинаковую семантику idempotent producer. Версии выбираются последними стабильными на момент реализации и фиксируются lock-файлами.

### Конфигурация и запуск

Хранилище обязательно: пустой `DATABASE__URL` (`payments`, `delivery`) или `MONGODB__URI` (`restaurants`) — ошибка конфигурации на старте, без молчаливого возврата к памяти. In-memory адаптеры остаются только в тестах. Relay дополнительно требует `KAFKA__BROKERS`; основной процесс к Kafka не подключается.

## Consequences

Три контекста получают durable state и надёжную публикацию Integration Events, что разблокирует consumers в `orders` (факты Courier Assignment, Payment, Restaurant Order). Появляются три новых процесса relay, миграционные процессы и две эксплуатируемые СУБД в тестовом окружении; MongoDB требует replica set даже локально.

Polling добавляет задержку не меньше интервала опроса и постоянную нагрузку запросами к Outbox; single active relay ограничивает пропускную способность одним экземпляром, как в `orders`. Head-of-line остановка пачки задерживает все последующие записи при сбое одной — это осознанная плата за порядок, сопровождаемая alert'ом на возраст старейшей записи.
